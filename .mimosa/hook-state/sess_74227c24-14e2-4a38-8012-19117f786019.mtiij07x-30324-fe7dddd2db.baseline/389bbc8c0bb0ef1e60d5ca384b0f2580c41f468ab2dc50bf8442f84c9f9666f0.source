//go:build !windows

// 非 Windows 平台暂不支持本机目录选择对话框
package hostdialog

import "errors"

// PickFolder 非 Windows 占位实现
func PickFolder(title string) (string, error) {
	return "", errors.New("目录选择对话框仅支持 Windows")
}
