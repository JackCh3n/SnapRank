// SnapRank 帧选 — 本地 AI 照片评分归档。
// 用法：
//
//	SnapRank.exe serve [--port 8787] [--no-open]   本地 Web 服务（默认）
//	SnapRank.exe run --dir ./photos [flags]        CLI 批量模式
//	SnapRank.exe version                           版本信息
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"snaprank/internal/archive"
	"snaprank/internal/config"
	"snaprank/internal/core"
	"snaprank/internal/hostdialog"
	"snaprank/internal/logutil"
	"snaprank/internal/pipeline"
	"snaprank/server"
)

// buildVersion 由 CI 通过 -ldflags "-X main.buildVersion=v2026_0830" 注入；
// 为空时使用 server.Version 的内置默认值。
var buildVersion = ""

func init() {
	if buildVersion != "" {
		server.Version = buildVersion
	}
}

func main() {
	args := os.Args[1:]
	// desktop 构建（wails build）下无参数启动即打开桌面窗口
	if len(args) == 0 && desktopBuild {
		runDesktop()
		return
	}
	if len(args) == 0 {
		serveCmd(args)
		return
	}
	switch args[0] {
	case "serve":
		serveCmd(args[1:])
	case "run":
		runCmd(args[1:])
	case "pickdir":
		// 内部子命令：弹出目录选择框并把结果写到 stdout（供服务端派生调用）。
		// 始终正常退出，结果/错误都经 stdout 传递（父进程解析首行）。
		dir, err := hostdialog.PickFolder()
		switch {
		case err != nil:
			fmt.Println("ERROR: " + err.Error())
		case dir == "":
			fmt.Println("CANCELLED")
		default:
			fmt.Println(dir)
		}
	case "version", "-v", "--version":
		fmt.Printf("SnapRank 帧选 %s\n", server.Version)
	case "help", "-h", "--help":
		usage()
	default:
		// 允许直接传 --port 等参数启动 serve
		serveCmd(args)
	}
}

func usage() {
	fmt.Print(`SnapRank 帧选 — 本地 AI 照片评分归档

  serve [--port 8787] [--no-open] [--data-dir DIR]   启动本地 Web 服务（默认）
  run --dir DIR [--model ID] [--out DIR] [--copy|--move]
      [--sample N] [--dry-run] [--yes] [--data-dir DIR]   CLI 批量模式
  version                                            版本信息
`)
}

// ---------- serve ----------

func serveCmd(args []string) {
	fs2 := flag.NewFlagSet("serve", flag.ExitOnError)
	port := fs2.Int("port", 8787, "监听端口")
	noOpen := fs2.Bool("no-open", false, "启动后不自动打开浏览器")
	dataDir := fs2.String("data-dir", "", "数据目录（默认 %LOCALAPPDATA%\\SnapRank）")
	fs2.Parse(args)

	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	c := mustCore(*dataDir)
	defer c.Close()
	defer logutil.Close()
	c.StartAutoBackup() // 每日自动备份数据库（保留最近 7 份）

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "端口监听失败 %s: %v\n", addr, err)
		os.Exit(1)
	}
	url := "http://" + addr
	fmt.Printf("帧选 SnapRank %s 已启动: %s （数据目录 %s）\n", server.Version, url, c.DataDir())
	logutil.Info("serve 启动 %s", url)

	if !*noOpen {
		go func() {
			time.Sleep(600 * time.Millisecond) // 等待 HTTP 就绪
			openBrowser(url)
		}()
	}

	srv := &http.Server{Handler: server.New(c).Handler()}
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "服务异常退出: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("服务已停止")
}

func mustCore(dataDir string) *core.Core {
	dir := dataDir
	if dir == "" {
		dir = config.DefaultDataDir()
	}
	if err := logutil.Init(dir); err != nil {
		fmt.Fprintf(os.Stderr, "日志初始化失败: %v\n", err)
	}
	c, err := core.New(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	return c
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	if err := cmd.Start(); err != nil {
		logutil.Info("打开浏览器失败: %v", err)
	}
}

// ---------- run（CLI） ----------

func runCmd(args []string) {
	fs2 := flag.NewFlagSet("run", flag.ExitOnError)
	dir := fs2.String("dir", "", "源图目录（必填）")
	model := fs2.String("model", "", "模型 ID（默认取配置）")
	out := fs2.String("out", "", "归档输出根目录（默认取配置）")
	move := fs2.Bool("move", false, "归档用移动（默认复制）")
	sample := fs2.Int("sample", 0, "抽样数量（只评前 N 张）")
	dryRun := fs2.Bool("dry-run", false, "只跑压缩+评分，不归档")
	yes := fs2.Bool("yes", false, "跳过归档确认")
	forceNew := fs2.Bool("force-new", false, "强制新建会话（不续跑）")
	fs2.Parse(args)

	if *dir == "" {
		fmt.Fprintln(os.Stderr, "缺少 --dir 参数")
		fs2.Usage()
		os.Exit(1)
	}
	if *move && *dryRun {
		fmt.Fprintln(os.Stderr, "--dry-run 与 --move 不能同时使用")
		os.Exit(1)
	}
	if *out != "" {
		cfg := mustLoadCfg()
		cfg.Paths.ArchiveRoot = *out
		if err := cfg.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "保存配置失败: %v\n", err)
			os.Exit(1)
		}
	}

	c := mustCore("")
	defer c.Close()
	defer logutil.Close()

	mode := archive.Copy
	if *move {
		mode = archive.Move
	}

	bus := c.Engine().Bus
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	res, err := c.Start(pipeline.StartOpts{
		Dir: *dir, Model: *model, SampleN: *sample, ForceNew: *forceNew,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "启动失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("会话 %s 开始处理 %s（模型 %s）", res.SessionID, *dir, c.GetCurrentModel())
	if res.Resumed {
		fmt.Printf("，续跑剩余 %d 张", res.Pending)
	}
	fmt.Println()

	var done *pipeline.DonePayload
	for ev := range ch {
		switch e := ev.Data.(type) {
		case pipeline.Progress:
			switch e.Status {
			case "scored":
				fmt.Printf("  [%d] %.1f 分  %s\n", e.Index, e.Score, e.File)
			case "parse_fail":
				fmt.Printf("  [%d] 解析失败  %s\n", e.Index, e.File)
			case "failed", "bad_image", "unsupported":
				fmt.Printf("  [%d] %s  %s（%s）\n", e.Index, e.Status, e.File, e.Error)
			}
		case pipeline.DonePayload:
			done = &e
		}
		if ev.Type == "done" {
			break
		}
	}
	if done == nil {
		return
	}
	if done.Stopped {
		fmt.Println("任务已停止（可重跑续传）")
		return
	}

	sum, err := c.Summary("")
	if err == nil && sum != nil {
		fmt.Printf("评分完成：均分 %.1f，最高 %.1f\n", sum.AvgScore, sum.MaxScore)
		for _, b := range []string{"35_精选", "34_良好", "33_一般", "30_待清理", "29_待复检"} {
			if n := sum.Buckets[b]; n > 0 {
				fmt.Printf("  %s: %d 张\n", b, n)
			}
		}
	}
	if *dryRun {
		fmt.Println("--dry-run：跳过归档")
		return
	}
	if !*yes {
		fmt.Print("确认归档？（回车=复制 / 输入 move=移动 / n=取消）: ")
		var ans string
		fmt.Scanln(&ans)
		switch ans {
		case "":
		case "move":
			mode = archive.Move
		default:
			fmt.Println("已取消归档")
			return
		}
	}
	as, err := c.Archive(string(mode), "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "归档失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("归档完成（%s）：放置 %d，跳过 %d，失败 %d → %s\n", as.Mode, as.Placed, as.Skipped, as.Failed, as.Dir)
	for _, e := range as.Errors {
		fmt.Println("  !", e)
	}
}

func mustLoadCfg() *config.Config {
	cfg, err := config.Load(config.DefaultDataDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	return cfg
}
