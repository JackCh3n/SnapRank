//go:build desktop

// 桌面壳（可选形态）：go build -tags desktop 时启用。
// 架构：本地 HTTP 服务（与 serve 形态同一套 server/core，HTTP/SSE 原样工作）
// + WebView2 原生窗口加载该地址——前端零改动，双击 exe 直接出窗口。
//
// 单实例策略：默认端口 8787；被占用说明已有实例在跑，此时不再起服务，
// 直接新开一个窗口指向现有实例（多窗口共用同一份数据与服务）。
package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	webview2 "github.com/jchv/go-webview2"

	"snaprank/internal/logutil"
	"snaprank/server"
)

const desktopBuild = true

const shellPort = 8787

func runDesktop() {
	url := ""
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", shellPort))
	if err != nil {
		// 端口被占：视为已有实例运行，新开窗口指向它
		url = fmt.Sprintf("http://127.0.0.1:%d", shellPort)
		logutil.Info("检测到已有实例（端口 %d 已占用），新开窗口指向现有服务", shellPort)
	} else {
		c := mustCore("")
		defer c.Close()
		defer logutil.Close()
		c.StartAutoBackup()
		// ReadHeaderTimeout 防慢连接占用；不设 WriteTimeout（SSE 长连接需要持续输出）
		srv := &http.Server{Handler: server.New(c).Handler(), ReadHeaderTimeout: 10 * time.Second}
		go func() { _ = srv.Serve(ln) }()
		url = "http://" + ln.Addr().String()
		logutil.Info("桌面壳启动 %s", url)
	}
	openShellWindow(url)
}

// openShellWindow 打开 WebView2 原生窗口并阻塞至窗口关闭；
// WebView2 运行时不可用时回退到系统默认浏览器。
func openShellWindow(url string) {
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  "帧选 SnapRank " + server.Version,
			Width:  1280,
			Height: 820,
			Center: true,
		},
	})
	if w == nil {
		fmt.Fprintln(os.Stderr, "WebView2 初始化失败：请安装 Microsoft WebView2 Runtime，已回退浏览器模式")
		openBrowser(url)
		time.Sleep(2 * time.Second)
		return
	}
	defer w.Terminate()
	w.SetTitle("帧选 SnapRank " + server.Version)
	w.Navigate(url)
	w.Run() // 阻塞至窗口关闭，进程随之退出（本地服务一并结束）
}
