// Package compress 本地压缩：预处理（EXIF 方向校正、缩边、统一转 JPEG）
// + 复用本地项目 DD鹅（ddgoose-go/ui，MozJPEG 引擎）做最终压缩。
// 压缩图仅作为模型输入与缩略图，绝不改动源图。
package compress

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/disintegration/imaging"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
	_ "image/gif"
	_ "image/png"

	"ddgoose-go/ui"

	"snaprank/internal/exiforient"
	"snaprank/internal/fp"
	"snaprank/internal/logutil"
)

// SupportedExts 支持解码的扩展名
var SupportedExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true,
	".gif": true, ".bmp": true, ".tif": true, ".tiff": true,
}

// UnsupportedExts 扫描时登记但不支持的格式（HEIC/RAW），明细中标记 unsupported
var UnsupportedExts = map[string]bool{
	".heic": true, ".heif": true, ".cr2": true, ".cr3": true, ".nef": true,
	".arw": true, ".dng": true, ".orf": true, ".rw2": true, ".raf": true,
}

// Engine 压缩引擎
type Engine struct {
	LibDir      string // DD鹅 lib 工具目录
	MaxEdge     int    // 压缩图最长边
	JPEGQuality int    // DD鹅 MozJPEG 质量
}

// NewEngine 构造；libDir 回退链：配置值 → exe 同级 lib\ → DD鹅 项目目录，
// 全部缺失时报错并提示配置。打包分发时 exe 旁带 lib\ 即可开箱即用。
func NewEngine(libDir string, maxEdge, quality int) (*Engine, error) {
	for _, dir := range libCandidates(libDir) {
		if libExists(dir) {
			return &Engine{LibDir: dir, MaxEdge: maxEdge, JPEGQuality: quality}, nil
		}
	}
	return nil, fmt.Errorf("DD鹅 lib 工具目录不存在（尝试: %s）；请在设置中配置 lib_dir，或把 lib 文件夹放到 exe 同级", libDir)
}

// libCandidates 按优先级返回候选 lib 目录
func libCandidates(configured string) []string {
	cands := []string{}
	if configured != "" {
		cands = append(cands, configured)
	}
	if exe, err := os.Executable(); err == nil {
		cands = append(cands, filepath.Join(filepath.Dir(exe), "lib"))
	}
	cands = append(cands, filepath.Join("..", "ddGoose-go", "lib"), `D:\wwwroot\wwwroot\ddGoose-go\lib`)
	return cands
}

func libExists(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, "cjpeg-mod.exe"))
	return err == nil && !st.IsDir()
}

// CacheName 压缩缓存文件名（指纹前 16 位，天然避免同名冲突）
func CacheName(fingerprint string) string {
	return fp.Short(fingerprint, 16) + ".jpg"
}

// ToCache 将源图压缩并输出到 cacheDir/<指纹>.jpg，返回压缩图路径。
// 已存在同指纹缓存时直接命中跳过（断点续跑）。
// 流程：① 直通拷贝（无需重编码时）或预处理到临时文件 → ② DD鹅 原地压缩 → ③ 落缓存。
func (e *Engine) ToCache(srcPath, fingerprint, cacheDir string) (string, error) {
	dst := filepath.Join(cacheDir, CacheName(fingerprint))
	if st, err := os.Stat(dst); err == nil && st.Size() > 0 {
		return dst, nil
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}

	tmpPath := filepath.Join(cacheDir, ".tmp_"+CacheName(fingerprint))
	defer os.Remove(tmpPath)
	defer os.Remove(tmpPath + ".tmp") // DD鹅 原地模式的中间产物

	// ① 准备临时文件：无需重编码的 JPEG 直通拷贝，其余预处理（EXIF/缩边/转 JPEG）
	needPreprocess := true
	if isJPEG(srcPath) {
		cfg, _, err := image.DecodeConfig(openFile(srcPath))
		if err != nil {
			return "", fmt.Errorf("解码失败（损坏或不受支持）: %w", err)
		}
		long := cfg.Width
		if cfg.Height > long {
			long = cfg.Height
		}
		needPreprocess = long > e.MaxEdge || readOrientation(srcPath) != 1
	}
	if needPreprocess {
		if err := e.preprocess(srcPath, tmpPath); err != nil {
			return "", err
		}
	} else if err := copyFile(srcPath, tmpPath); err != nil {
		return "", err
	}

	// ② DD鹅（MozJPEG）原地压缩临时文件；失败或未变小时保留预处理产物（不视为错误）
	if err := e.ddgooseCompress(tmpPath); err != nil {
		logutil.Info("DD鹅 压缩回退为预处理产物 %s: %v", filepath.Base(srcPath), err)
	}

	// ③ 落缓存
	if err := os.Rename(tmpPath, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// ddgooseCompress 调用 DD鹅（MozJPEG）对临时文件原地压缩
func (e *Engine) ddgooseCompress(path string) error {
	opts := ui.DefaultOptions()
	opts.OutputMode = ui.OutputReplace // 原地：内部经 .tmp 中转后替换
	opts.Format = ui.FormatOriginal
	opts.JpegQuality = e.JPEGQuality
	opts.WriteMarker = false // 缓存图不写魔数标记
	opts.Threads = 1         // 并发由流水线控制
	c := ui.NewCompressor(e.LibDir, opts)
	_, err := c.Compress(path)
	return err
}

// preprocess 解码 → EXIF 方向校正 → 缩边 → 重编码 JPEG q95（剥离 EXIF）
func (e *Engine) preprocess(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("打开文件失败: %w", err)
	}
	ori := readOrientation(src)
	img, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		return fmt.Errorf("解码失败（损坏或不受支持）: %w", err)
	}
	img = applyOrientation(img, ori)
	if e.MaxEdge > 0 {
		if w, h := img.Bounds().Dx(), img.Bounds().Dy(); w > e.MaxEdge || h > e.MaxEdge {
			img = imaging.Fit(img, e.MaxEdge, e.MaxEdge, imaging.Lanczos)
		}
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	// 白底合成透明通道（PNG→JPEG）
	if err := jpeg.Encode(out, flatten(img), &jpeg.Options{Quality: 95}); err != nil {
		return fmt.Errorf("编码 JPEG 失败: %w", err)
	}
	return nil
}

// ToThumbDataURI 生成缩略图 data URI（预览用，最长边 thumbEdge）
func (e *Engine) LoadThumb(compressedPath string, thumbEdge int) (image.Image, error) {
	f, err := os.Open(compressedPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	if thumbEdge > 0 {
		img = imaging.Fit(img, thumbEdge, thumbEdge, imaging.Linear)
	}
	return img, nil
}

func isJPEG(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".jpg") || strings.EqualFold(filepath.Ext(path), ".jpeg")
}

// copyFile 直通拷贝（无需重编码时保留原始字节）
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
	return out.Close()
}

func openFile(p string) *os.File {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	return f
}

func readOrientation(path string) int {
	f := openFile(path)
	if f == nil {
		return 1
	}
	defer f.Close()
	if isJPEG(path) {
		return exiforient.Read(f)
	}
	return 1
}

// applyOrientation 按 EXIF Orientation 旋转/翻转（imaging 旋转方向为逆时针）
func applyOrientation(img image.Image, ori int) image.Image {
	switch ori {
	case 2:
		return imaging.FlipH(img)
	case 3:
		return imaging.Rotate180(img)
	case 4:
		return imaging.FlipV(img)
	case 5:
		return imaging.Transpose(img)
	case 6:
		return imaging.Rotate270(img) // 逆时针270° = 顺时针90°
	case 7:
		return imaging.Transverse(img)
	case 8:
		return imaging.Rotate90(img)
	default:
		return img
	}
}

// flatten 将透明背景合成到白底（JPEG 无透明通道）
func flatten(img image.Image) image.Image {
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Over)
	return dst
}
