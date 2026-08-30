package ui

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// errNoImprovement 原地压缩后产物未变小：保留原文件（不替换、不写标记），
// 由 Compress 层转换为“成功但无收益”的结果，避免把未压缩的文件标记为已压缩。
var errNoImprovement = errors.New("压缩后文件未变小，保留原文件")

// CompressFormat 压缩输出格式
type CompressFormat int

const (
	FormatOriginal CompressFormat = iota // 保持原格式
	FormatWebpLossless                   // WebP 无损（预留）
	FormatWebpLossy                      // WebP 有损（预留）
	FormatAvif                           // AVIF 转换（Squoosh 增强引擎）
	FormatJxl                            // JPEG XL 转换（Squoosh 增强引擎）
)

// 压缩标记魔数，追加在图片文件末尾（JPEG EOI / PNG IEND / GIF Trailer 之后）
var compressionMarker = []byte("\x00DDGOOSE_V1")

// IsCompressed 检查文件是否已被 DD鹅 压缩过（只读取文件末尾魔数，避免加载整个文件）
func IsCompressed(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil || fi.Size() < int64(len(compressionMarker)) {
		return false
	}

	// 只读取文件末尾的魔数部分
	tail := make([]byte, len(compressionMarker))
	_, err = f.ReadAt(tail, fi.Size()-int64(len(compressionMarker)))
	if err != nil && err != io.EOF {
		return false
	}
	return bytes.Equal(tail, compressionMarker)
}

// markCompressed 在文件末尾追加压缩标记魔数
func markCompressed(path string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(compressionMarker)
	return err
}

// OutputMode 输出模式
type OutputMode int

const (
	OutputReplace OutputMode = iota // 原图替换（原地覆盖）
	OutputCustom                    // 输出到自定义目录
	OutputBackup                    // 备份后替换（原图旁保留 .ddgoose-bak 侧车便于还原）
	OutputSuffix                    // 另存为新文件（原名 + .min）
)

// CompressOptions 压缩选项
type CompressOptions struct {
	OutputMode   OutputMode
	OutputDir    string
	Format       CompressFormat
	JpegQuality  int // JPEG 质量 1-100
	PngQuality   int // PNG 质量 1-100（映射 pngquant --quality=0-Q）
	GifLossy     int // GIF 有损级别 0-200（越高压缩越强）
	Threads      int // 并行线程数, 默认 4, 最大 16
	WebpQuality  int // WebP 转换质量 1-100
	Preset       string // 预设档: recommended/wechat/hifi/extreme/custom
	WriteMarker  bool   // 是否在文件末尾写入压缩标记魔数
	Squoosh      bool   // 是否启用 Squoosh 增强引擎（OxiPNG/AVIF/JPEG XL）
}

// DefaultOptions 返回默认选项（DD鹅推荐档，即原默认压缩质量）
func DefaultOptions() CompressOptions {
	return CompressOptions{
		OutputMode:   OutputReplace,
		Format:       FormatOriginal,
		JpegQuality:  67,
		PngQuality:   90,
		GifLossy:     30,
		Threads:      4,
		WebpQuality:  67,
		Preset:       "recommended",
		WriteMarker:  true,
		Squoosh:      true, // 默认启用 Squoosh 增强引擎（OxiPNG），可在设置中关闭
	}
}

// Compressor 图片压缩器
type Compressor struct {
	libDir string
	opts   CompressOptions
}

// NewCompressor 创建压缩器
func NewCompressor(libDir string, opts CompressOptions) *Compressor {
	return &Compressor{
		libDir: libDir,
		opts:   opts,
	}
}

// Compress 压缩单张图片，返回输出文件路径
func (c *Compressor) Compress(inputPath string) (string, error) {
	inputPath = filepath.Clean(inputPath)
	ext := strings.ToLower(filepath.Ext(inputPath))

	originalInfo, err := os.Stat(inputPath)
	if err != nil {
		return "", fmt.Errorf("无法读取文件: %w", err)
	}
	originalSize := originalInfo.Size()

	var outputPath string
	switch ext {
	case ".jpg", ".jpeg":
		outputPath, err = c.compressJPEG(inputPath)
	case ".png":
		outputPath, err = c.compressPNG(inputPath)
	case ".gif":
		outputPath, err = c.compressGIF(inputPath)
	default:
		return "", fmt.Errorf("不支持的图片格式: %s", ext)
	}
	if err != nil {
		if errors.Is(err, errNoImprovement) {
			// 原地压缩无收益：原图未被改动、未写标记，视为成功结果返回原路径
			return inputPath, nil
		}
		return "", err
	}

	// 非原地模式（另存/自定义目录）：产物反而变大则删除产物、保留原图（不写标记）
	if outputPath != inputPath && originalSize > 0 && c.opts.Format == FormatOriginal {
		if fi, statErr := os.Stat(outputPath); statErr == nil && fi.Size() > originalSize {
			_ = os.Remove(outputPath)
			return inputPath, nil
		}
	}

	// 写入压缩标记魔数（受设置开关控制，关闭则不写入）
	if c.opts.WriteMarker {
		if err := markCompressed(outputPath); err != nil {
			LogError("追加压缩标记失败: %s: %v", outputPath, err)
		}
	}
	return outputPath, nil
}

// getOutputPath 根据输出模式计算输出路径
func (c *Compressor) getOutputPath(inputPath string) string {
	baseName := filepath.Base(inputPath)
	targetDir := filepath.Dir(inputPath)

	if c.opts.OutputMode == OutputCustom && c.opts.OutputDir != "" {
		targetDir = c.opts.OutputDir
		// 确保输出目录存在
		os.MkdirAll(targetDir, 0755)
	}

	// 处理格式转换（先转扩展名）
	ext := filepath.Ext(baseName)
	switch c.opts.Format {
	case FormatWebpLossless, FormatWebpLossy:
		baseName = strings.TrimSuffix(baseName, ext) + ".webp"
	case FormatAvif:
		baseName = strings.TrimSuffix(baseName, ext) + ".avif"
	case FormatJxl:
		baseName = strings.TrimSuffix(baseName, ext) + ".jxl"
	}

	// 另存为新文件模式：在文件名后追加 .min
	if c.opts.OutputMode == OutputSuffix {
		ext2 := filepath.Ext(baseName)
		baseName = strings.TrimSuffix(baseName, ext2) + ".min" + ext2
	}

	return filepath.Clean(filepath.Join(targetDir, baseName))
}

// compressJPEG 压缩 JPEG（cjpeg-mod 读取输入文件，-outfile 指定输出文件）
func (c *Compressor) compressJPEG(inputPath string) (string, error) {
	if c.opts.Format != FormatOriginal {
		return c.convertFormat(inputPath)
	}

	outputPath := c.getOutputPath(inputPath)
	cjpegPath := filepath.Join(c.libDir, "cjpeg-mod.exe")

	// 原地覆盖先输出到临时文件；压缩失败或未变小都会清理临时文件，绝不污染原图
	workOutput := outputPath
	isInPlace := (outputPath == inputPath)
	if isInPlace {
		workOutput = inputPath + ".tmp"
	}
	success := false
	defer func() {
		if !success && workOutput != inputPath {
			os.Remove(workOutput)
		}
	}()

	// cjpeg-mod: 最后一个参数是输入文件，输出写入 stdout 重定向到 workOutput
	args := []string{
		"-optimize",
		"-quant-baseline",
		"-quality", fmt.Sprintf("%d", c.opts.JpegQuality),
		"-sample", "1x1",
		"-quant-table", "3",
		inputPath,
	}

	outFile, err := os.Create(workOutput)
	if err != nil {
		return "", fmt.Errorf("创建输出文件失败: %w", err)
	}

	cmd := exec.Command(cjpegPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stdout = outFile
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf
	if err := cmd.Run(); err != nil {
		outFile.Close()
		return "", fmt.Errorf("JPEG 压缩失败: %s\n%w", stderrBuf.String(), err)
	}

	// 在 Windows 上重命名前必须关闭输出文件句柄
	if err := outFile.Close(); err != nil {
		return "", fmt.Errorf("关闭输出文件失败: %w", err)
	}
	success = true

	// 原地覆盖：产物未更小则保留原文件（不替换、不写标记）
	if isInPlace {
		if orig, _ := os.Stat(inputPath); orig != nil && orig.Size() > 0 {
			if oi, statErr := os.Stat(workOutput); statErr == nil && oi.Size() >= orig.Size() {
				os.Remove(workOutput)
				return inputPath, errNoImprovement
			}
		}
		if err := os.Rename(workOutput, inputPath); err != nil {
			return "", fmt.Errorf("替换原文件失败: %w", err)
		}
		return inputPath, nil
	}

	return outputPath, nil
}

// compressPNG 压缩 PNG（pngquant → advpng → advdef 三步）
func (c *Compressor) compressPNG(inputPath string) (string, error) {
	if c.opts.Format != FormatOriginal {
		return c.convertFormat(inputPath)
	}

	outputPath := c.getOutputPath(inputPath)
	pngquantPath := filepath.Join(c.libDir, "pngquant-mod.exe")
	advpngPath := filepath.Join(c.libDir, "advpng.exe")
	advdefPath := filepath.Join(c.libDir, "advdef.exe")

	isInPlace := (outputPath == inputPath)

	// 使用临时路径作为 pngquant 输出；原地覆盖时最终再替换回原文件
	workPath := outputPath
	if isInPlace {
		workPath = inputPath + ".pngquant.tmp"
	}
	success := false
	defer func() {
		if !success && workPath != inputPath {
			os.Remove(workPath)
		}
	}()

	// 第一步：pngquant 有损压缩（--quality=10-98 实测参数）
	pngquantArgs := []string{
		inputPath,
		"--output=" + workPath,
		"--quality=" + fmt.Sprintf("10-%d", c.opts.PngQuality),
		"-f",
		"--strip",
	}

	cmd := exec.Command(pngquantPath, pngquantArgs...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("pngquant 压缩失败: %s\n%w", string(out), err)
	}

	// 第二步：Squoosh 增强引擎使用 OxiPNG（-a -o 6 -s --quiet 实测参数），否则使用 advpng+advdef
	if c.opts.Squoosh {
		oxipngPath := filepath.Join(c.libDir, "oxipng.exe")
		if _, err := os.Stat(oxipngPath); err == nil {
			cmd2 := exec.Command(oxipngPath, "-a", "-o", "6", "-s", "--quiet", workPath)
			cmd2.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			cmd2.CombinedOutput() // 失败不致命，继续输出
		}
	} else {
		cmd2 := exec.Command(advpngPath, "-z", "-3", workPath)
		cmd2.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		cmd2.CombinedOutput() // 失败不致命

		// 第三步：advdef 进一步优化 deflate
		if _, err := os.Stat(advdefPath); err == nil {
			cmd3 := exec.Command(advdefPath, "-z", "-4", workPath)
			cmd3.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			cmd3.CombinedOutput() // 失败不致命
		}
	}
	success = true

	// 原地覆盖：产物未更小则保留原文件（不替换、不写标记）
	if isInPlace {
		if orig, _ := os.Stat(inputPath); orig != nil && orig.Size() > 0 {
			if oi, statErr := os.Stat(workPath); statErr == nil && oi.Size() >= orig.Size() {
				os.Remove(workPath)
				return inputPath, errNoImprovement
			}
		}
		if err := os.Rename(workPath, inputPath); err != nil {
			return "", fmt.Errorf("替换原文件失败: %w", err)
		}
		return inputPath, nil
	}

	return outputPath, nil
}

// compressGIF 压缩 GIF
func (c *Compressor) compressGIF(inputPath string) (string, error) {
	if c.opts.Format != FormatOriginal {
		return c.convertFormat(inputPath)
	}

	outputPath := c.getOutputPath(inputPath)
	gifsiclePath := filepath.Join(c.libDir, "gifsicle.exe")

	isInPlace := (outputPath == inputPath)
	workOutput := outputPath
	if isInPlace {
		workOutput = inputPath + ".gifsicle.tmp"
	}
	success := false
	defer func() {
		if !success && workOutput != inputPath {
			os.Remove(workOutput)
		}
	}()

	args := []string{
		"-O5",
		fmt.Sprintf("--lossy=%d", c.opts.GifLossy),
		"-o", workOutput,
		inputPath,
	}

	cmd := exec.Command(gifsiclePath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("GIF 压缩失败: %s\n%w", string(out), err)
	}
	success = true

	// 原地覆盖：产物未更小则保留原文件（不替换、不写标记）
	if isInPlace {
		if orig, _ := os.Stat(inputPath); orig != nil && orig.Size() > 0 {
			if oi, statErr := os.Stat(workOutput); statErr == nil && oi.Size() >= orig.Size() {
				os.Remove(workOutput)
				return inputPath, errNoImprovement
			}
		}
		if err := os.Rename(workOutput, inputPath); err != nil {
			return "", fmt.Errorf("替换原文件失败: %w", err)
		}
		return inputPath, nil
	}

	return outputPath, nil
}

// convertFormat 将图片转换为目标格式
func (c *Compressor) convertFormat(inputPath string) (string, error) {
	outputPath := c.getOutputPath(inputPath)
	ext := strings.ToLower(filepath.Ext(inputPath))

	switch c.opts.Format {
	case FormatWebpLossless, FormatWebpLossy:
		// GIF 使用 gif2webp 保持动画
		if ext == ".gif" {
			return c.gifToWebp(inputPath, outputPath)
		}
		return c.imageToWebp(inputPath, outputPath)
	case FormatAvif:
		if ext == ".gif" {
			return "", fmt.Errorf("AVIF 暂不支持 GIF 动画转换")
		}
		return c.imageToAvif(inputPath, outputPath)
	case FormatJxl:
		if ext == ".gif" {
			return "", fmt.Errorf("JPEG XL 暂不支持 GIF 动画转换")
		}
		return c.imageToJxl(inputPath, outputPath)
	default:
		return "", fmt.Errorf("不支持的格式转换目标: %v", c.opts.Format)
	}
}

// imageToWebp 使用 cwebp 转换静态图片
func (c *Compressor) imageToWebp(inputPath, outputPath string) (string, error) {
	cwebpPath := filepath.Join(c.libDir, "cwebp.exe")
	if _, err := os.Stat(cwebpPath); os.IsNotExist(err) {
		return "", fmt.Errorf("cwebp.exe 未找到，无法转换 WebP")
	}

	q := c.opts.WebpQuality
	if q <= 0 {
		q = 80
	}
	args := []string{"-q", strconv.Itoa(q), inputPath, "-o", outputPath}

	if c.opts.Format == FormatWebpLossless {
		args = []string{"-lossless", "-z", "9", inputPath, "-o", outputPath}
	}

	cmd := exec.Command(cwebpPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("WebP 转换失败: %s\n%w", string(out), err)
	}
	return outputPath, nil
}

// gifToWebp 使用 gif2webp 转换 GIF（保持动画）
func (c *Compressor) gifToWebp(inputPath, outputPath string) (string, error) {
	gif2webpPath := filepath.Join(c.libDir, "gif2webp.exe")
	if _, err := os.Stat(gif2webpPath); os.IsNotExist(err) {
		// 回退到 cwebp（会丢失动画）
		return c.imageToWebp(inputPath, outputPath)
	}

	q := c.opts.WebpQuality
	if q <= 0 {
		q = 80
	}
	args := []string{"-lossy", "-q", strconv.Itoa(q), inputPath, "-o", outputPath}

	if c.opts.Format == FormatWebpLossless {
		args = []string{"-lossless", "-q", "100", inputPath, "-o", outputPath}
	}

	cmd := exec.Command(gif2webpPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("GIF→WebP 转换失败: %s\n%w", string(out), err)
	}
	return outputPath, nil
}

// imageToAvif 使用 avifenc 将静态图片转换为 AVIF
func (c *Compressor) imageToAvif(inputPath, outputPath string) (string, error) {
	avifencPath := filepath.Join(c.libDir, "avifenc.exe")
	if _, err := os.Stat(avifencPath); os.IsNotExist(err) {
		return "", fmt.Errorf("avifenc.exe 未找到，无法转换 AVIF")
	}

	q := c.opts.JpegQuality
	if q <= 0 {
		q = 80
	}
	// avifenc 的 --min/--max 同时设置为同一质量，等价固定质量编码
	// alpha 质量使用默认值，避免部分版本要求 min/max alpha 成对出现
	args := []string{
		"--min", strconv.Itoa(q),
		"--max", strconv.Itoa(q),
		"--", inputPath, outputPath,
	}

	cmd := exec.Command(avifencPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("AVIF 转换失败: %s\n%w", string(out), err)
	}
	return outputPath, nil
}

// imageToJxl 使用 cjxl 将静态图片转换为 JPEG XL
func (c *Compressor) imageToJxl(inputPath, outputPath string) (string, error) {
	cjxlPath := filepath.Join(c.libDir, "cjxl.exe")
	if _, err := os.Stat(cjxlPath); os.IsNotExist(err) {
		return "", fmt.Errorf("cjxl.exe 未找到，无法转换 JPEG XL")
	}

	q := c.opts.JpegQuality
	if q <= 0 {
		q = 80
	}
	args := []string{
		"-q", strconv.Itoa(q),
		inputPath, outputPath,
	}

	cmd := exec.Command(cjxlPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("JPEG XL 转换失败: %s\n%w", string(out), err)
	}
	return outputPath, nil
}
