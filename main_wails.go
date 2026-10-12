//go:build desktop

// 桌面壳（可选形态）：go build -tags desktop 时启用。
// 架构：本地 HTTP 服务（与 serve 形态同一套 server/core，HTTP/SSE 原样工作）
// + WebView2 原生窗口加载该地址——前端零改动，双击 exe 直接出窗口。
//
// 端口策略：从固定端口 8787 起向后找——空闲则自起服务；被占用则探测是否为
// 已有的 SnapRank 实例（是→复用，实现单实例多窗口）；是其它应用则继续向后。
// 不能无验证复用占用者：其它项目长期占用 8787 时会把别人的页面当成 SnapRank。
package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	webview2 "github.com/jchv/go-webview2"

	"snaprank/internal/logutil"
	"snaprank/server"
)

const desktopBuild = true

const shellPort = 8787

// 端口扫描上限：8787 起 52 个端口内总有一个可用
const shellPortMax = shellPort + 51

func runDesktop() {
	ln, url, reuse, err := pickDesktopAddress()
	if err != nil {
		logutil.Error("%v", err)
		return // windowsgui 无控制台，落日志即可
	}
	if reuse {
		logutil.Info("检测到已有 SnapRank 实例 %s，新开窗口指向现有服务", url)
		openShellWindow(url)
		return
	}
	c := mustCore("")
	defer c.Close()
	defer logutil.Close()
	c.StartAutoBackup()
	// ReadHeaderTimeout 防慢连接占用；不设 WriteTimeout（SSE 长连接需要持续输出）
	srv := &http.Server{Handler: server.New(c).Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	logutil.Info("桌面壳启动 %s", url)
	openShellWindow(url)
}

// pickDesktopAddress 返回监听器与窗口地址；reuse=true 表示端口上已是
// SnapRank 实例，直接开窗口复用（ln 为 nil）
func pickDesktopAddress() (ln net.Listener, url string, reuse bool, err error) {
	for p := shellPort; p <= shellPortMax; p++ {
		addr := fmt.Sprintf("127.0.0.1:%d", p)
		l, e := net.Listen("tcp", addr)
		if e == nil {
			return l, "http://" + addr, false, nil
		}
		if isSnapRankInstance(p) {
			return nil, "http://" + addr, true, nil
		}
	}
	return nil, "", false, fmt.Errorf("本地端口 %d-%d 均被占用，无法启动桌面壳", shellPort, shellPortMax)
}

// isSnapRankInstance 探测端口上跑的是不是本应用：
// /api/state 返回 200 且含特征字段 currentModel（其它服务 404/401/字段不同）
func isSnapRankInstance(port int) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/state", port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode == http.StatusOK && strings.Contains(string(body), `"currentModel"`)
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
