// gen_samples 生成 E2E 验证用的测试样片，覆盖边界情况：
// 不同色彩/明暗（mock 分数差异）、超大图（缩边）、EXIF 旋转、批内重复同名、
// 损坏文件、BADJSON 触发解析失败、PNG 透明通道、HEIC 假文件（不支持格式）。
package main

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand"
	"os"
	"path/filepath"
)

func gradient(w, h int, a, b color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			t := float64(x+y) / float64(w+h)
			img.Set(x, y, color.RGBA{
				R: uint8(float64(a.R)*(1-t) + float64(b.R)*t),
				G: uint8(float64(a.G)*(1-t) + float64(b.G)*t),
				B: uint8(float64(a.B)*(1-t) + float64(b.B)*t),
				A: 255,
			})
		}
	}
	return img
}

func noisy(w, h int, seed int64, base color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewSource(seed))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			j := int(r.Intn(60)) - 30
			img.Set(x, y, color.RGBA{clamp8(int(base.R) + j), clamp8(int(base.G) + j), clamp8(int(base.B) + j), 255})
		}
	}
	return img
}

func clamp8(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func saveJPEG(p string, img image.Image, q int) error {
	os.MkdirAll(filepath.Dir(p), 0o755)
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	return jpeg.Encode(f, img, &jpeg.Options{Quality: q})
}

func savePNG(p string, img image.Image) error {
	os.MkdirAll(filepath.Dir(p), 0o755)
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// withOrientation 在 JPEG 前部插入 EXIF Orientation 段
func withOrientation(data []byte, ori int) []byte {
	exif := []byte{
		'E', 'x', 'i', 'f', 0, 0,
		'I', 'I', 42, 0, 8, 0, 0, 0,
		1, 0,
		0x12, 0x01, 3, 0, 1, 0, 0, 0, byte(ori), 0, 0, 0,
		0, 0, 0, 0,
	}
	l := len(exif) + 2
	app1 := []byte{0xFF, 0xE1, byte(l >> 8), byte(l & 0xFF)}
	app1 = append(app1, exif...)
	out := append([]byte{}, data[:2]...)
	out = append(out, app1...)
	return append(out, data[2:]...)
}

func main() {
	out := flag.String("out", "samples", "输出目录")
	flag.Parse()

	// 常规 12 张：不同色彩倾向与噪点（mock 模式下产生不同分数）
	colors := []color.RGBA{
		{30, 60, 120, 255}, {200, 80, 40, 255}, {40, 140, 90, 255}, {180, 170, 40, 255},
		{100, 40, 140, 255}, {230, 230, 230, 255}, {20, 20, 20, 255}, {0, 150, 200, 255},
		{250, 120, 160, 255}, {60, 60, 60, 255}, {140, 200, 60, 255}, {90, 130, 180, 255},
	}
	for i, c := range colors {
		var img image.Image
		if i%2 == 0 {
			img = gradient(1600, 1067, c, color.RGBA{255 - c.R, 255 - c.G, 255 - c.B, 255})
		} else {
			img = noisy(1200, 800, int64(i+1), c)
		}
		if err := saveJPEG(filepath.Join(*out, fmt.Sprintf("IMG_%04d.jpg", 1000+i)), img, 85); err != nil {
			panic(err)
		}
	}

	// 超大图：最长边 3000px，触发缩边至 2048
	big := gradient(3000, 2000, color.RGBA{200, 120, 30, 255}, color.RGBA{30, 90, 160, 255})
	if err := saveJPEG(filepath.Join(*out, "IMG_BIG_3000px.jpg"), big, 90); err != nil {
		panic(err)
	}

	// EXIF 旋转：左侧 1/4 红条 + Orientation 6（校正后红条应转到顶部）
	rot := gradient(1600, 1200, color.RGBA{250, 245, 235, 255}, color.RGBA{220, 215, 205, 255})
	for y := 0; y < 1200; y++ {
		for x := 0; x < 400; x++ {
			rot.Set(x, y, color.RGBA{230, 30, 30, 255})
		}
	}
	var buf bytes.Buffer
	jpeg.Encode(&buf, rot, &jpeg.Options{Quality: 90})
	os.MkdirAll(*out, 0o755)
	if err := os.WriteFile(filepath.Join(*out, "IMG_ROTATED_EXIF6.jpg"), withOrientation(buf.Bytes(), 6), 0o644); err != nil {
		panic(err)
	}

	// 批内重复：不同子目录、同名同内容
	dup := gradient(1200, 800, color.RGBA{10, 90, 160, 255}, color.RGBA{240, 220, 160, 255})
	saveJPEG(filepath.Join(*out, "day1", "DUP_IMG_0001.jpg"), dup, 85)
	saveJPEG(filepath.Join(*out, "day2", "DUP_IMG_0001.jpg"), dup, 85)

	// PNG 透明通道：左半透明（转 JPEG 应合成白底）
	tr := image.NewRGBA(image.Rect(0, 0, 800, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 800; x++ {
			if x < 400 {
				tr.Set(x, y, color.RGBA{0, 0, 0, 0})
			} else {
				tr.Set(x, y, color.RGBA{180, 60, 160, 255})
			}
		}
	}
	savePNG(filepath.Join(*out, "IMG_TRANSPARENT.png"), tr)

	// 损坏 JPEG（随机字节）
	os.MkdirAll(*out, 0o755)
	os.WriteFile(filepath.Join(*out, "IMG_CORRUPT.jpg"), []byte("\xFF\xD8\xFF\xE0broken-not-a-jpeg\x01\x02\x03"), 0o644)

	// BADJSON 文件名 → mock 返回畸形输出，触发 parse_fail → 待复检链路
	bj := gradient(1200, 800, color.RGBA{160, 30, 30, 255}, color.RGBA{30, 30, 30, 255})
	saveJPEG(filepath.Join(*out, "IMG_BADJSON_01.jpg"), bj, 85)

	// HEIC 假文件（应为 unsupported）
	os.WriteFile(filepath.Join(*out, "IMG_IPHONE.heic"), []byte("fake heic payload"), 0o644)

	// 不相关文件（应被忽略）
	os.WriteFile(filepath.Join(*out, "readme.txt"), []byte("not an image"), 0o644)

	fmt.Println("样片已生成:", *out)
}
