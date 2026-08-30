// Package logutil 提供写往用户目录的按天轮转日志与标准错误双写。
package logutil

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

var (
	mu       sync.Mutex
	file     *os.File
	logDir   string
	keep     = 7
	dayKey   string
	toStderr = true
)

// Init 初始化日志目录（datadir/logs），并清理过期日志
func Init(dataDir string) error {
	mu.Lock()
	defer mu.Unlock()
	logDir = filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}
	pruneOldLocked()
	return openForTodayLocked()
}

// SetQuiet 关闭标准错误输出（供 GUI 场景）
func SetQuiet(q bool) {
	mu.Lock()
	toStderr = !q
	mu.Unlock()
}

func openForTodayLocked() error {
	dayKey = time.Now().Format("2006-01-02")
	p := filepath.Join(logDir, "snaprank-"+dayKey+".log")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	file = f
	return nil
}

func writeLocked(level, format string, args ...interface{}) {
	if file == nil || dayKey != time.Now().Format("2006-01-02") {
		if logDir != "" {
			if file != nil {
				file.Close()
			}
			if err := openForTodayLocked(); err != nil {
				file = nil
			}
		}
	}
	line := fmt.Sprintf("%s [%s] %s\n", time.Now().Format("2006-01-02 15:04:05.000"), level, fmt.Sprintf(format, args...))
	if file != nil {
		file.WriteString(line)
	}
	if toStderr {
		io.WriteString(os.Stderr, line)
	}
}

// Info 记录普通日志
func Info(format string, args ...interface{}) {
	mu.Lock()
	defer mu.Unlock()
	writeLocked("INFO", format, args...)
}

// Error 记录错误日志
func Error(format string, args ...interface{}) {
	mu.Lock()
	defer mu.Unlock()
	writeLocked("ERROR", format, args...)
}

// Close 关闭日志文件
func Close() {
	mu.Lock()
	defer mu.Unlock()
	if file != nil {
		file.Close()
		file = nil
	}
}

func pruneOldLocked() {
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) <= keep {
		return
	}
	sort.Strings(names)
	for _, n := range names[:len(names)-keep] {
		os.Remove(filepath.Join(logDir, n))
	}
}
