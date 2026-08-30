package exiforient

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

// buildJPEGWithOrientation 构造带指定 Orientation 的 JPEG（手工拼 EXIF APP1）
func buildJPEGWithOrientation(t *testing.T, ori int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	src := buf.Bytes()

	// 最小 EXIF：II 字节序，IFD0 仅一个 Orientation 条目
	exif := []byte{
		'E', 'x', 'i', 'f', 0, 0,
		'I', 'I', 42, 0,
		8, 0, 0, 0, // IFD0 偏移
		1, 0, // 条目数 1
		0x12, 0x01, // tag 0x0112 Orientation
		3, 0, // type SHORT
		1, 0, 0, 0, // count 1
		byte(ori), 0, 0, 0, // value
		0, 0, 0, 0, // 下一个 IFD 偏移
	}
	app1Len := len(exif) + 2
	app1 := []byte{0xFF, 0xE1, byte(app1Len >> 8), byte(app1Len & 0xFF)}
	app1 = append(app1, exif...)

	// 插在 SOI 之后
	out := append([]byte{}, src[:2]...)
	out = append(out, app1...)
	out = append(out, src[2:]...)
	return out
}

func TestReadOrientation(t *testing.T) {
	for ori := 1; ori <= 8; ori++ {
		got := Read(bytes.NewReader(buildJPEGWithOrientation(t, ori)))
		if got != ori {
			t.Errorf("orientation %d: got %d", ori, got)
		}
	}
}

func TestReadNoExif(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var buf bytes.Buffer
	jpeg.Encode(&buf, img, nil)
	if got := Read(bytes.NewReader(buf.Bytes())); got != 1 {
		t.Fatalf("无 EXIF 应为 1，got %d", got)
	}
}

func TestReadNonJPEG(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.png")
	os.WriteFile(p, []byte{0x89, 'P', 'N', 'G'}, 0o644)
	data, _ := os.ReadFile(p)
	if got := Read(bytes.NewReader(data)); got != 1 {
		t.Fatalf("非 JPEG 应为 1，got %d", got)
	}
}
