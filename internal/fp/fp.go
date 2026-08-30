// Package fp 计算文件内容指纹（SHA-256），用于去重、断点续跑与跨会话评分缓存。
package fp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// OfFile 返回文件内容的 SHA-256 十六进制指纹
func OfFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Short 返回指纹前 n 位（用于压缩缓存命名）
func Short(fingerprint string, n int) string {
	if len(fingerprint) <= n {
		return fingerprint
	}
	return fingerprint[:n]
}

// OfBytes 返回字节内容的指纹（测试用）
func OfBytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// OfFileAt 为已打开文件计算指纹（避免重复打开）
func OfFileAt(f io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("计算指纹失败: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
