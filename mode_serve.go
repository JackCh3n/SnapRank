//go:build !desktop

// 非 desktop 构建时的回退：直接进入本地 Web 服务模式。
package main

import "fmt"

const desktopBuild = false

func runDesktop() {
	fmt.Println("当前二进制未包含 Wails 桌面壳（需 wails build / -tags desktop），转入 Web 服务模式。")
	serveCmd(nil)
}
