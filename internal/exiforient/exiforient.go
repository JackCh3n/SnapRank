// Package exiforient 从 JPEG 的 EXIF 中解析 Orientation 标签（无需第三方依赖），
// 用于压缩前方向校正；重编码后 EXIF 随之剥离（隐私与体积收益）。
package exiforient

import (
	"encoding/binary"
	"errors"
	"io"
)

var errNotFound = errors.New("orientation not found")

const (
	markerAPP1 = 0xE1 // APP1 段标记（0xFFE1 的低字节）
	orientTag  = 0x0112
)

// Read 从 JPEG 流中读取 Orientation（1-8）；无 EXIF 或无该标签返回 1。
// 仅支持 JPEG；其他格式一律返回 1。
func Read(r io.Reader) int {
	v, err := read(r)
	if err != nil || v < 1 || v > 8 {
		return 1
	}
	return v
}

func read(r io.Reader) (int, error) {
	// SOI
	var u16 = make([]byte, 2)
	if _, err := io.ReadFull(r, u16); err != nil {
		return 0, err
	}
	if u16[0] != 0xFF || u16[1] != 0xD8 {
		return 0, errNotFound
	}
	for {
		// marker
		if _, err := io.ReadFull(r, u16); err != nil {
			return 0, err
		}
		if u16[0] != 0xFF {
			return 0, errNotFound
		}
		marker := u16[1]
		// 独立标记无长度段
		if marker == 0xD8 || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) {
			continue
		}
		if _, err := io.ReadFull(r, u16); err != nil {
			return 0, err
		}
		segLen := int(binary.BigEndian.Uint16(u16))
		if segLen < 2 {
			return 0, errNotFound
		}
		seg := make([]byte, segLen-2)
		if _, err := io.ReadFull(r, seg); err != nil {
			return 0, err
		}
		if marker == markerAPP1 && len(seg) > 12 && string(seg[:6]) == "Exif\x00\x00" {
			return parseTIFFOrientation(seg[6:])
		}
		if marker == 0xDA { // SOS：进入熵编码数据，后面不再是段结构
			return 0, errNotFound
		}
	}
}

func parseTIFFOrientation(tiff []byte) (int, error) {
	if len(tiff) < 8 {
		return 0, errNotFound
	}
	var bo binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0, errNotFound
	}
	if bo.Uint16(tiff[2:4]) != 42 {
		return 0, errNotFound
	}
	ifd0 := int(bo.Uint32(tiff[4:8]))
	if ifd0+2 > len(tiff) {
		return 0, errNotFound
	}
	n := int(bo.Uint16(tiff[ifd0 : ifd0+2]))
	entryOff := ifd0 + 2
	for i := 0; i < n; i++ {
		off := entryOff + i*12
		if off+12 > len(tiff) {
			return 0, errNotFound
		}
		if bo.Uint16(tiff[off:off+2]) == orientTag {
			return int(bo.Uint16(tiff[off+8 : off+10])), nil
		}
	}
	return 0, errNotFound
}
