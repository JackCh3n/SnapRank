// Package archive 阶段二归档：按档位复制/移动照片，处理同名冲突与跨卷移动，
// 并导出带 UTF-8 BOM 的 report.csv（保证 Excel 打开中文不乱码）。
package archive

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"snaprank/internal/fp"
	"snaprank/internal/store"
)

// Mode 归档方式
type Mode string

const (
	Copy Mode = "copy"
	Move Mode = "move"
)

// ValidMode 是否合法
func ValidMode(m Mode) bool { return m == Copy || m == Move }

// Place 将一张照片放置到 sessionDir/bucket 下，返回目标路径。
// 冲突策略：目标已存在且指纹相同 → 跳过；指纹不同 → 追加序号 name (2).ext。
func Place(sessionDir, bucket, srcPath, fingerprint string, mode Mode) (dest string, skipped bool, err error) {
	destDir := filepath.Join(sessionDir, bucket)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", false, err
	}
	dest = filepath.Join(destDir, filepath.Base(srcPath))

	if _, err := os.Stat(dest); err == nil {
		same, ferr := sameContent(dest, fingerprint)
		if ferr != nil {
			return "", false, ferr
		}
		if same {
			return dest, true, nil
		}
		dest = uniqueName(destDir, srcPath)
	}

	switch mode {
	case Move:
		if err := os.Rename(srcPath, dest); err != nil {
			// 跨卷/权限问题回退 copy+delete
			if cerr := copyFile(srcPath, dest); cerr != nil {
				return "", false, fmt.Errorf("移动失败: %w", err)
			}
			if rerr := os.Remove(srcPath); rerr != nil {
				return dest, false, fmt.Errorf("复制成功但删除源文件失败: %w", rerr)
			}
		}
	default:
		if err := copyFile(srcPath, dest); err != nil {
			return "", false, err
		}
	}
	return dest, false, nil
}

func sameContent(path, fingerprint string) (bool, error) {
	f, err := fp.OfFile(path)
	if err != nil {
		return false, err
	}
	return f == fingerprint, nil
}

func uniqueName(dir, srcPath string) string {
	base := filepath.Base(srcPath)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 2; ; i++ {
		cand := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand
		}
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if info, err := os.Stat(src); err == nil {
		os.Chtimes(dst, info.ModTime(), info.ModTime())
	}
	return out.Sync()
}

// ReportRow CSV 明细行
type ReportRow struct {
	Filename   string
	SrcPath    string
	Score      float64
	Dims       *store.Dims
	Tags       []string
	Strength   string
	Weakness   string
	Model      string
	Bucket     string
	DurationMs int64
	Status     string
	UpdatedAt  string
}

// WriteReportCSV 导出 report.csv（UTF-8 BOM，Excel 友好）
func WriteReportCSV(sessionDir string, rows []*ReportRow) (string, error) {
	p := filepath.Join(sessionDir, "report.csv")
	f, err := os.Create(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	// UTF-8 BOM
	if _, err := f.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return "", err
	}
	w := csv.NewWriter(f)
	if err := w.Write([]string{"文件名", "源路径", "总分", "技术质量", "构图", "内容情感", "色彩", "标签", "优点", "不足", "模型", "档位", "耗时ms", "状态", "时间"}); err != nil {
		return "", err
	}
	for _, r := range rows {
		dims := []string{"", "", "", ""}
		if r.Dims != nil {
			dims = []string{f1(r.Dims.Technique), f1(r.Dims.Composition), f1(r.Dims.Content), f1(r.Dims.Color)}
		}
		rec := []string{
			r.Filename, r.SrcPath, f1(r.Score),
			dims[0], dims[1], dims[2], dims[3],
			strings.Join(r.Tags, " "),
			r.Strength, r.Weakness, r.Model, r.Bucket,
			strconv.FormatInt(r.DurationMs, 10), r.Status, r.UpdatedAt,
		}
		if err := w.Write(rec); err != nil {
			return "", err
		}
	}
	w.Flush()
	return p, w.Error()
}

func f1(v float64) string {
	return strconv.FormatFloat(v, 'f', 1, 64)
}

// SessionStamp 批次时间戳（目录命名备选）
func SessionStamp() string {
	return time.Now().Format("20060102_150405")
}
