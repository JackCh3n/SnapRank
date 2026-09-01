//go:build windows

// Package hostdialog 弹出本机系统对话框（目录选择）。
// 使用 Windows IFileDialog（FOS_PICKFOLDERS）：与资源管理器一致的现代
// 文件选择器交互。通过 ole32 CoCreateInstance(INPROC_SERVER) 创建
// （必须是进程内组件，走 CLSCTX_SERVER 会被代理进程导致 vtable 崩溃）。
// 服务进程无前台窗口，弹出期间用辅助线程把对话框持续拉到最前。
package hostdialog

import (
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"golang.org/x/sys/windows"
)

var (
	ole32                     = windows.NewLazySystemDLL("ole32.dll")
	procCoCreateInstance      = ole32.NewProc("CoCreateInstance")
	user32                    = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows           = user32.NewProc("EnumWindows")
	procIsWindowVisible       = user32.NewProc("IsWindowVisible")
	procGetWindowTextW        = user32.NewProc("GetWindowTextW")
	procSetForegroundWindow   = user32.NewProc("SetForegroundWindow")
	procBringWindowToTop      = user32.NewProc("BringWindowToTop")
	procGetForegroundWindow   = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcID = user32.NewProc("GetWindowThreadProcessId")
	procAttachThreadInput     = user32.NewProc("AttachThreadInput")
	procGetCurrentThreadId    = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCurrentThreadId")
	enumWindowsProc           uintptr
)

const (
	clsctxInprocServer = 0x1
	fosPickFolders     = 0x20
	fosForceFilesystem = 0x40
	sigdnFilesysPath   = 0x80058000
	hrCancelled        = 0x800704C7 // ERROR_CANCELLED
	dialogTitle        = "选择照片目录"
)

var (
	clsidFileOpenDialog = ole.NewGUID("{DC1C5A9C-E88A-4DDE-A5A1-60F82A20AEF7}")
	iidFileOpenDialog   = ole.NewGUID("{D57C7288-D4AD-4768-BE02-9D969532D960}")
)

// IFileOpenDialog vtable（真实声明顺序：Advise/Unadvise 在 GetFileTypeIndex 之后，
// MSDN 的方法字母表不等于 vtable 顺序——之前漏了这两个导致 SetOptions 位置错位崩溃）
type vtblFileOpenDialog struct {
	QueryInterface      uintptr
	AddRef              uintptr
	Release             uintptr
	Show                uintptr
	SetFileTypes        uintptr
	SetFileTypeIndex    uintptr
	GetFileTypeIndex    uintptr
	Advise              uintptr
	Unadvise            uintptr
	SetOptions          uintptr
	GetOptions          uintptr
	SetDefaultFolder    uintptr
	SetFolder           uintptr
	GetFolder           uintptr
	GetCurrentSelection uintptr
	SetFileName         uintptr
	GetFileName         uintptr
	SetTitle            uintptr
	SetOkButtonLabel    uintptr
	SetFileNameLabel    uintptr
	GetResult           uintptr
	AddPlace            uintptr
	SetDefaultExtension uintptr
	Close               uintptr
	SetClientGuid       uintptr
	ClearClientData     uintptr
	SetFilter           uintptr
	// IFileOpenDialog 追加的 GetResults/GetSelectedItems 未使用
}

type iFileOpenDialog struct {
	lpVtbl *vtblFileOpenDialog
}

// IShellItem vtable 前 6 项（GetDisplayName 为第 6 个方法）
type vtblShellItem struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
	BindToHandler  uintptr
	GetParent      uintptr
	GetDisplayName uintptr
}

// PickFolder 弹出目录选择框；用户取消返回空串。
// 阻塞至用户选择完成，调用方需在后台 goroutine 中调用。
func PickFolder() (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		return "", fmt.Errorf("COM 初始化失败: %w", err)
	}
	defer ole.CoUninitialize()

	var d *iFileOpenDialog
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(clsidFileOpenDialog)),
		0,
		uintptr(clsctxInprocServer),
		uintptr(unsafe.Pointer(iidFileOpenDialog)),
		uintptr(unsafe.Pointer(&d)))
	if hr != 0 || d == nil {
		return "", fmt.Errorf("创建目录选择框失败: 0x%x", hr)
	}
	defer syscall.SyscallN(d.lpVtbl.Release, uintptr(unsafe.Pointer(d)))

	// 只允许选目录
	if hr, _, _ := syscall.SyscallN(d.lpVtbl.SetOptions, uintptr(unsafe.Pointer(d)),
		uintptr(fosPickFolders|fosForceFilesystem)); hr != 0 {
		return "", fmt.Errorf("SetOptions 失败: 0x%x", hr)
	}
	if t16, err := windows.UTF16PtrFromString(dialogTitle); err == nil {
		syscall.SyscallN(d.lpVtbl.SetTitle, uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(t16)))
	}

	// 前台辅助：后台进程弹的对话框默认被埋住，循环把它拉到最前
	stop := make(chan struct{})
	go bringToFrontLoop(dialogTitle, stop)
	defer close(stop)

	// Show 阻塞；用户取消返回 hrCancelled
	hr, _, _ = syscall.SyscallN(d.lpVtbl.Show, uintptr(unsafe.Pointer(d)), 0)
	if hr == hrCancelled {
		return "", nil
	}
	if hr != 0 {
		return "", fmt.Errorf("Show 失败: 0x%x", hr)
	}

	// GetResult 取所选 IShellItem；对象首 8 字节是 vtable 指针，
	// GetDisplayName 是 vtable 第 6 项（下标 5）。必须先读 vtable 指针再取函数——
	// 直接把对象当 vtable 结构用会跳到对象内部数据（此前的崩溃根因）。
	var item unsafe.Pointer
	if hr, _, _ = syscall.SyscallN(d.lpVtbl.GetResult, uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(&item))); hr != 0 || item == nil {
		return "", fmt.Errorf("GetResult 失败: 0x%x", hr)
	}
	itemVtbl := *(*unsafe.Pointer)(item)
	defer syscall.SyscallN(*(*uintptr)(unsafe.Pointer(uintptr(itemVtbl) + 2*8)), uintptr(item)) // Release

	var pPath unsafe.Pointer
	getDisplayName := *(*uintptr)(unsafe.Pointer(uintptr(itemVtbl) + 5*8))
	if hr, _, _ = syscall.SyscallN(getDisplayName, uintptr(item), uintptr(sigdnFilesysPath), uintptr(unsafe.Pointer(&pPath))); hr != 0 || pPath == nil {
		return "", fmt.Errorf("GetDisplayName 失败: 0x%x", hr)
	}
	defer ole.CoTaskMemFree(uintptr(pPath))

	path := windows.UTF16PtrToString((*uint16)(pPath))
	if path == "" {
		return "", fmt.Errorf("所选目录路径为空")
	}
	return strings.TrimRight(path, "\\"), nil
}

// bringToFrontLoop 循环查找标题匹配的可见窗口并强制置前
func bringToFrontLoop(title string, stop chan struct{}) {
	for {
		select {
		case <-stop:
			return
		default:
		}
		if h := findWindowByTitle(title); h != 0 {
			foregroundToTop(h)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// EnumWindows 回调只能通过 syscall.NewCallback 创建一次（有全局数量上限，
// 在轮询循环里反复创建会触发 "too many callback functions" 致命错误使进程退出）
var (
	findTargetTitle string
	findResult      uintptr
)

func enumWindowsCallback(hwnd, lparam uintptr) uintptr {
	visible, _, _ := procIsWindowVisible.Call(hwnd)
	if visible == 0 {
		return 1
	}
	buf := make([]uint16, 128)
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n > 0 && windows.UTF16ToString(buf[:n]) == findTargetTitle {
		findResult = hwnd
		return 0
	}
	return 1
}

func init() {
	enumWindowsProc = syscall.NewCallback(enumWindowsCallback)
}

func findWindowByTitle(title string) uintptr {
	findTargetTitle = title
	findResult = 0
	procEnumWindows.Call(enumWindowsProc, 0)
	return findResult
}

// foregroundToTop 通过 AttachThreadInput 抢占前台（绕过后台进程限制）
func foregroundToTop(h uintptr) {
	fg, _, _ := procGetForegroundWindow.Call()
	cur, _, _ := procGetCurrentThreadId.Call()
	fgTid, _, _ := procGetWindowThreadProcID.Call(fg)
	if fg != 0 && fgTid != 0 && fgTid != cur {
		procAttachThreadInput.Call(cur, fgTid, 1)
	}
	procBringWindowToTop.Call(h)
	procSetForegroundWindow.Call(h)
	if fg != 0 && fgTid != 0 && fgTid != cur {
		procAttachThreadInput.Call(cur, fgTid, 0)
	}
}
