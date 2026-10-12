//go:build !windows

// 非 Windows 平台暂不支持本机目录选择对话框
package hostdialog

import "errors"

// PickFolders 非 Windows 占位实现
func PickFolders() ([]string, error) {
	return nil, errors.New("目录选择对话框仅支持 Windows")
}
