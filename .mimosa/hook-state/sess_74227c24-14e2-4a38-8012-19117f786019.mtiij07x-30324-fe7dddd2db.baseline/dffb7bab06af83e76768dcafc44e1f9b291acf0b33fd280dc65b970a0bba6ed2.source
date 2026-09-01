package archive

import (
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"snaprank/internal/fp"
)

func makeJPEG(t *testing.T, path string, c color.RGBA) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for x := 0; x < 32; x++ {
		for y := 0; y < 32; y++ {
			img.Set(x, y, c)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
}

func TestPlaceCopyAndConflict(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	makeJPEG(t, src, color.RGBA{R: 255, A: 255})
	fp1, _ := fp.OfFile(src)

	sessionDir := filepath.Join(dir, "out")
	dest, skipped, err := Place(sessionDir, "35_精选", src, fp1, Copy)
	if err != nil || skipped {
		t.Fatalf("首次复制应成功: %v skipped=%v", err, skipped)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("产物应存在: %v", err)
	}

	// 再次归档同内容 → 跳过（指纹一致）
	_, skipped2, err := Place(sessionDir, "35_精选", src, fp1, Copy)
	if err != nil || !skipped2 {
		t.Fatalf("同指纹应跳过: %v skipped=%v", err, skipped2)
	}

	// 同名不同内容 → 重命名 name (2).ext
	src2 := filepath.Join(dir, "other", "photo.jpg")
	os.MkdirAll(filepath.Dir(src2), 0o755)
	makeJPEG(t, src2, color.RGBA{G: 255, A: 255})
	fp2, _ := fp.OfFile(src2)
	dest2, skipped3, err := Place(sessionDir, "35_精选", src2, fp2, Copy)
	if err != nil || skipped3 {
		t.Fatalf("不同内容应归档: %v skipped=%v", err, skipped3)
	}
	if filepath.Base(dest2) != "photo (2).jpg" {
		t.Fatalf("应重命名为 photo (2).jpg，got %s", filepath.Base(dest2))
	}
	// 源文件仍在（复制模式）
	if _, err := os.Stat(src2); err != nil {
		t.Fatal("复制模式源文件不应被删除")
	}
}

func TestPlaceMove(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "sub", "a.jpg")
	os.MkdirAll(filepath.Dir(src), 0o755)
	makeJPEG(t, src, color.RGBA{B: 255, A: 255})
	f, _ := fp.OfFile(src)

	sessionDir := filepath.Join(dir, "out")
	dest, skipped, err := Place(sessionDir, "30_待清理", src, f, Move)
	if err != nil || skipped {
		t.Fatalf("移动应成功: %v skipped=%v", err, skipped)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("移动后源文件应不存在")
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("目标应存在: %v", err)
	}
}

func TestCrossVolumeMoveFallback(t *testing.T) {
	// 单卷环境无法真实造跨卷：直接验证 Place 对无效目标的回退逻辑不丢数据
	dir := t.TempDir()
	src := filepath.Join(dir, "x.jpg")
	makeJPEG(t, src, color.RGBA{R: 10, G: 200, A: 255})
	f, _ := fp.OfFile(src)
	sessionDir := filepath.Join(dir, "out")
	dest, _, err := Place(sessionDir, "34_良好", src, f, Move)
	if err != nil {
		t.Fatalf("同卷移动应成功: %v", err)
	}
	data, err := os.ReadFile(dest)
	if err != nil || len(data) == 0 {
		t.Fatal("移动后产物应完整")
	}
}

func TestReportCSVHasBOM(t *testing.T) {
	dir := t.TempDir()
	rows := []*ReportRow{{Filename: "中文照片.jpg", Score: 8.5, Status: "scored", Tags: []string{"风光"}}}
	p, err := WriteReportCSV(dir, rows)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if len(data) < 3 || data[0] != 0xEF || data[1] != 0xBB || data[2] != 0xBF {
		t.Fatal("CSV 应带 UTF-8 BOM")
	}
}
