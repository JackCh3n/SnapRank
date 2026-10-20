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
	if err := placeTo(srcPath, dest, mode); err != nil {
		return "", false, err
	}
	return dest, false, nil
}

// PlaceScored 分值归档放置（同名不同内容时的区分命名）：
// ①主名冲突 → ②「主名_分值.扩展」→ 仍冲突 → ③「主名_分值_哈希前3位.扩展」
// → 仍冲突 → ④「主名_分值_哈希后3位.扩展」→ 极端情况退回「名 (2).ext」序号。
// 每个候选名都先做内容比对：已存在且指纹相同视为已归档（跳过），只有内容
// 确实不同才换下一个名字。分值归档只处理已评分照片，score 恒有效。
func PlaceScored(sessionDir, bucket, srcPath, fingerprint string, score float64, mode Mode) (dest string, skipped bool, err error) {
	destDir := filepath.Join(sessionDir, bucket)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", false, err
	}
	base := filepath.Base(srcPath)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	scoreStr := strconv.FormatFloat(score, 'f', 1, 64)
	head3, tail3 := fingerprint, fingerprint
	if len(head3) > 3 {
		head3 = head3[:3]
		tail3 = tail3[len(tail3)-3:]
	}
	cands := []string{
		base,
		stem + "_" + scoreStr + ext,
		stem + "_" + scoreStr + "_" + head3 + ext,
		stem + "_" + scoreStr + "_" + tail3 + ext,
	}
	for _, name := range cands {
		dest = filepath.Join(destDir, name)
		if _, err := os.Stat(dest); err == nil {
			same, ferr := sameContent(dest, fingerprint)
			if ferr != nil {
				return "", false, ferr
			}
			if same {
				return dest, true, nil // 同一份内容已归档过
			}
			continue // 同名不同内容：换下一个候选名
		}
		if err := placeTo(srcPath, dest, mode); err != nil {
			return "", false, err
		}
		return dest, false, nil
	}
	dest = uniqueName(destDir, srcPath) // 前3/后3 都撞上（概率极低）：退回序号
	if err := placeTo(srcPath, dest, mode); err != nil {
		return "", false, err
	}
	return dest, false, nil
}

// placeTo 把源文件落到目标路径：移动（跨卷/占用回退复制+删除）或复制。
func placeTo(srcPath, dest string, mode Mode) error {
	switch mode {
	case Move:
		if err := os.Rename(srcPath, dest); err != nil {
			// 跨卷/权限问题回退 copy+delete
			if cerr := copyFile(srcPath, dest); cerr != nil {
				return fmt.Errorf("移动失败: %w", err)
			}
			if rerr := os.Remove(srcPath); rerr != nil {
				return fmt.Errorf("复制成功但删除源文件失败: %w", rerr)
			}
		}
	default:
		return copyFile(srcPath, dest)
	}
	return nil
}

// RawSiblingExts 同名伴生文件扩展名（RAW/HEIC 等）：删除/归档/还原时联动处理
var RawSiblingExts = []string{".raf", ".cr2", ".cr3", ".nef", ".arw", ".dng", ".orf", ".rw2", ".heic", ".heif"}

// rawSiblings 列出与 stem（不含扩展名的完整路径）同名的伴生文件（.raf/.cr2…），
// 排除自身扩展名（selfExt）；按真实文件名返回（保留原名大小写）。
func rawSiblings(stem, selfExt string) []string {
	dir := filepath.Dir(stem)
	want := strings.ToLower(filepath.Base(stem))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := filepath.Ext(name)
		if strings.EqualFold(ext, selfExt) {
			continue // 排除自身
		}
		matched := false
		for _, x := range RawSiblingExts {
			if strings.EqualFold(x, ext) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		if strings.ToLower(strings.TrimSuffix(name, ext)) == want {
			out = append(out, filepath.Join(dir, name))
		}
	}
	return out
}

// PlaceRaws 归档时把与主图同名的伴生文件（RAW/HEIC）按同样的方式放到归档位置，
// 文件名跟随已放置的主图（destPath）主名，保持成对命名；目标已有同名伴生文件时
// 不覆盖（视为已归档）。返回放置数、跳过数与错误列表。
func PlaceRaws(destPath, srcPath string, mode Mode) (moved, skipped int, errs []string) {
	if destPath == "" || srcPath == "" {
		return 0, 0, nil
	}
	destStem := strings.TrimSuffix(destPath, filepath.Ext(destPath))
	srcStem := strings.TrimSuffix(srcPath, filepath.Ext(srcPath))
	for _, srcRaw := range rawSiblings(srcStem, filepath.Ext(srcPath)) {
		destRaw := destStem + filepath.Ext(srcRaw)
		if _, err := os.Stat(destRaw); err == nil {
			skipped++
			continue
		}
		var err error
		if mode == Move {
			err = MoveTo(srcRaw, destRaw)
		} else {
			err = copyFile(srcRaw, destRaw)
		}
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", filepath.Base(srcRaw), err))
			continue
		}
		moved++
	}
	return moved, skipped, errs
}

// RestoreRaws 还原归档时把归档目录里与主图同名的伴生文件一起移回原目录，
// 命名跟随还原后的主图（destPhotoPath）；目标已有同名文件时不覆盖。
func RestoreRaws(archivedPhotoPath, destPhotoPath string) (restored int, errs []string) {
	if archivedPhotoPath == "" || destPhotoPath == "" {
		return 0, nil
	}
	destStem := strings.TrimSuffix(destPhotoPath, filepath.Ext(destPhotoPath))
	srcStem := strings.TrimSuffix(archivedPhotoPath, filepath.Ext(archivedPhotoPath))
	for _, srcRaw := range rawSiblings(srcStem, filepath.Ext(archivedPhotoPath)) {
		destRaw := destStem + filepath.Ext(srcRaw)
		if _, err := os.Stat(destRaw); err == nil {
			continue // 原目录已有同名伴生文件：不覆盖
		}
		if err := MoveTo(srcRaw, destRaw); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", filepath.Base(srcRaw), err))
			continue
		}
		restored++
	}
	return restored, errs
}

// MoveTo 把源文件移动到目标路径（自动建目录；跨卷/被占用时回退复制+删除）。
// 还原归档用：把归档目录里的文件移回原路径。
func MoveTo(src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dest); err == nil {
		return nil
	}
	if cerr := copyFile(src, dest); cerr != nil {
		return cerr
	}
	return os.Remove(src)
}

// SameContent 文件内容是否与指纹一致（还原归档按内容匹配用）
func SameContent(path, fingerprint string) bool {
	same, err := sameContent(path, fingerprint)
	return err == nil && same
}

// UniqueName 目标目录下同名的可用路径（冲突时加序号）
func UniqueName(dir, srcPath string) string { return uniqueName(dir, srcPath) }

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
