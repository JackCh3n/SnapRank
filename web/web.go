// Package web 内嵌前端构建产物（Vite 输出到本包 dist/ 目录，随二进制分发）。
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// Dist 返回前端文件系统（dist 根）
func Dist() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return distFS
	}
	return sub
}
