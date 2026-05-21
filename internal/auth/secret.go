// Package auth 提供 token、secret 的安全随机生成与比对原语。
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	// EnrollTokenPrefix 让客户端能一眼识别这是 enrollment token。
	EnrollTokenPrefix = "ot_"
	// AdminTokenPrefix 标记 admin Web UI 的长期凭证。
	AdminTokenPrefix = "oat_"
	// enrollTokenBytes 是 token 内含的随机字节数，base32 后约 52 字符可见。
	enrollTokenBytes = 32
	// tunnelSecretBytes 是设备隧道密码字节数，base64 后约 88 字符。
	tunnelSecretBytes = 64
)

// 使用 RFC 4648 无填充的 base32（小写）便于命令行复制。
var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewEnrollmentToken 生成形如 `ot_xxxxxxxx...` 的一次性 token。
func NewEnrollmentToken() (string, error) {
	return newPrefixedToken(EnrollTokenPrefix)
}

// NewAdminToken 生成形如 `oat_xxxxxxxx...` 的长期 admin token（Web UI 凭证）。
func NewAdminToken() (string, error) {
	return newPrefixedToken(AdminTokenPrefix)
}

func newPrefixedToken(prefix string) (string, error) {
	buf := make([]byte, enrollTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成 token: %w", err)
	}
	return prefix + strings.ToLower(b32.EncodeToString(buf)), nil
}

// NewTunnelSecret 生成设备隧道密码。
func NewTunnelSecret() (string, error) {
	buf := make([]byte, tunnelSecretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成 tunnel secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// NewDeviceID 生成 16 字节的随机 hex 设备 ID。
func NewDeviceID() (string, error) {
	return NewRandomID()
}

// NewRandomID 生成 16 字节随机 hex，用作各类资源（device/service/token）的内部 ID。
func NewRandomID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成随机 ID: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// HashSecret 对 token / secret 做 sha256，输出 hex。
// 仅用于存储，调用方使用 ConstantTimeEqual 比对避免时序泄漏。
func HashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// ConstantTimeEqual 是 subtle.ConstantTimeCompare 的字符串包装。
func ConstantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// ValidateEnrollTokenFormat 在做 hash 之前做基本格式校验，避免无意义的 DB 查询。
func ValidateEnrollTokenFormat(s string) error {
	if !strings.HasPrefix(s, EnrollTokenPrefix) {
		return fmt.Errorf("enrollment token 必须以 %q 开头", EnrollTokenPrefix)
	}
	body := strings.TrimPrefix(s, EnrollTokenPrefix)
	if _, err := b32.DecodeString(strings.ToUpper(body)); err != nil {
		return fmt.Errorf("enrollment token 编码非法: %w", err)
	}
	return nil
}
