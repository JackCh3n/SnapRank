package main

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/go-ole/go-ole"
	"golang.org/x/sys/windows"
)

var (
	ole32                = windows.NewLazySystemDLL("ole32.dll")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")

	clsidFileOpenDialog = ole.NewGUID("{DC1C5A9C-E88A-4DDE-A5A1-60F82A20AEF7}")
	iidFileOpenDialog   = ole.NewGUID("{D57C7288-D4AD-4768-BE02-9D969532D960}")
)

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
}

type iFileOpenDialog struct {
	lpVtbl *vtblFileOpenDialog
}

func main() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		fmt.Println("CoInit:", err)
		return
	}
	defer ole.CoUninitialize()

	var d *iFileOpenDialog
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(clsidFileOpenDialog)),
		0,
		1, // CLSCTX_INPROC_SERVER
		uintptr(unsafe.Pointer(iidFileOpenDialog)),
		uintptr(unsafe.Pointer(&d)))
	fmt.Printf("CoCreateInstance hr=0x%x d=%v\n", hr, d != nil)
	if hr != 0 {
		return
	}
	defer syscall.SyscallN(d.lpVtbl.Release, uintptr(unsafe.Pointer(d)))

	var opts uint32
	hr, _, _ = syscall.SyscallN(d.lpVtbl.GetOptions, uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(&opts)))
	fmt.Printf("GetOptions hr=0x%x opts=0x%x\n", hr, opts)

	hr, _, _ = syscall.SyscallN(d.lpVtbl.SetOptions, uintptr(unsafe.Pointer(d)), uintptr(0x60))
	fmt.Printf("SetOptions hr=0x%x\n", hr)

	t16, _ := windows.UTF16PtrFromString("probe")
	hr, _, _ = syscall.SyscallN(d.lpVtbl.SetTitle, uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(t16)))
	fmt.Printf("SetTitle hr=0x%x\n", hr)
}
