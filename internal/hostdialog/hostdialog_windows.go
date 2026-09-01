//go:build windows

// Package hostdialog 弹出本机系统对话框（目录选择）。
// 服务运行在本机时，后端代为弹出系统目录选择框（PowerShell WinForms，
// STA 线程 + TopMost 确保置前显示），用户选完返回绝对路径给前端页面。
package hostdialog

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

// PickFolder 弹出目录选择框；用户取消返回空串。
// 阻塞至用户选择完成，调用方需在后台 goroutine 中调用。
func PickFolder(title string) (string, error) {
	title = strings.ReplaceAll(title, "'", "''")
	script := `
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
Add-Type -AssemblyName System.Windows.Forms | Out-Null
$f = New-Object System.Windows.Forms.FolderBrowserDialog
$f.Description = '` + title + `'
$f.ShowNewFolderButton = $false
$owner = New-Object System.Windows.Forms.Form -Property @{ TopMost = $true; ShowInTaskbar = $false }
if ($f.ShowDialog($owner) -eq [System.Windows.Forms.DialogResult]::OK) { [Console]::Out.Write($f.SelectedPath) }
`
	cmd := exec.Command("powershell", "-NoProfile", "-STA", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	out, err := cmd.Output()
	if err != nil {
		// 用户取消时 ExitCode 为 0、无输出；仅真实错误才上报
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 0 {
			return "", nil
		}
		return "", fmt.Errorf("打开目录选择框失败: %w", err)
	}
	return strings.TrimRight(string(out), "\\"), nil
}
