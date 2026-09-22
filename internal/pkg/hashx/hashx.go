// Package hashx 封装口令哈希与内容哈希。
package hashx

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"

	"golang.org/x/crypto/bcrypt"
)

// HashPassword 用 bcrypt 生成口令哈希。
func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("生成口令哈希失败: %w", err)
	}
	return string(b), nil
}

// VerifyPassword 校验口令。
func VerifyPassword(hashed, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hashed), []byte(plain)) == nil
}

// NewContentHasher 返回内容哈希器（SHA-256）。文件去重与秒传都基于它。
func NewContentHasher() hash.Hash { return sha256.New() }

// Sum 返回十六进制摘要。
func Sum(h hash.Hash) string { return hex.EncodeToString(h.Sum(nil)) }

// HashReader 边读边算，返回内容哈希与字节数。
func HashReader(r io.Reader) (string, int64, error) {
	h := NewContentHasher()
	n, err := io.Copy(h, r)
	if err != nil {
		return "", 0, err
	}
	return Sum(h), n, nil
}

// RandomToken 生成 n 字节的随机串（十六进制表示，长度为 2n）。
func RandomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成随机串失败: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// 分享码使用去掉易混字符的字母表，避免 0/O、1/l 抄错。
const shareAlphabet = "abcdefghijkmnpqrstuvwxyz23456789"

// ShareCode 生成分享码。
func ShareCode(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成分享码失败: %w", err)
	}
	out := make([]byte, n)
	for i, b := range buf {
		out[i] = shareAlphabet[int(b)%len(shareAlphabet)]
	}
	return string(out), nil
}
