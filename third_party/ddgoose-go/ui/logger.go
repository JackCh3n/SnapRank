package ui

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"time"
)

var logFile *os.File

// keepLogFiles 日志目录最多保留的日志文件数，超过则清理最旧的
const keepLogFiles = 20

// InitLogger 初始化日志文件
func InitLogger() {
	execPath, _ := os.Executable()
	cwd, _ := os.Getwd()
	// 优先 exe 同级 logs 目录，创建失败回退到工作目录 logs
	logDir := filepath.Join(filepath.Dir(execPath), "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		logDir = filepath.Join(cwd, "logs")
		os.MkdirAll(logDir, 0755)
	}
	// 清理旧日志，避免 logs 目录无限增长
	pruneOldLogs(logDir, keepLogFiles)

	logPath := filepath.Join(logDir, fmt.Sprintf("ddgoose_%s.log", time.Now().Format("20060102_150405")))
	var err error
	logFile, err = os.Create(logPath)
	if err != nil {
		logFile = os.Stderr
	}
	log.SetOutput(logFile)
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	log.Println("=== DD鹅 启动 ===")
	log.Printf("可执行文件路径: %s", execPath)
	log.Printf("工作目录: %s", cwd)
}

// pruneOldLogs 清理日志目录，只保留最近 keep 份 ddgoose_*.log，避免无限增长
func pruneOldLogs(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var logs []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "ddgoose_") && strings.HasSuffix(e.Name(), ".log") {
			logs = append(logs, filepath.Join(dir, e.Name()))
		}
	}
	if len(logs) <= keep {
		return
	}
	sort.Strings(logs) // 文件名含时间戳，字典序即时间序
	for _, p := range logs[:len(logs)-keep] {
		os.Remove(p)
	}
}

// CloseLogger 关闭日志
func CloseLogger() {
	log.Println("=== DD鹅 退出 ===")
	if logFile != nil && logFile != os.Stderr {
		logFile.Close()
	}
}

// LogInfo 记录信息
func LogInfo(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	log.Println("[INFO]", msg)
}

// LogError 记录错误
func LogError(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	log.Println("[ERROR]", msg)
}

// SafeRun 安全执行，捕获 panic
func SafeRun(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[PANIC] %v\nStack:\n%s", r, string(debug.Stack()))
			// 写入一个紧急日志
			if logFile != nil && logFile != os.Stderr {
				logFile.Sync()
			}
			// 显示错误对话框（如果可能）
			msg := fmt.Sprintf("程序发生严重错误:\n%v\n\n详细日志已保存到 logs 目录", r)
			_ = msg
		}
	}()
	fn()
}
