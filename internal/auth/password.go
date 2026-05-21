package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// 密码 hash 用 argon2id，OWASP 推荐参数（2024）。
// 参数固定，但编码字符串里携带，方便日后调整。
//
// 输出格式（PHC string，跟 libsodium / passlib 兼容）：
//   $argon2id$v=19$m=65536,t=3,p=4$<salt-b64>$<hash-b64>
//
// 个人单用户场景，cost 偏保守不影响登录手感（每次 ≈ 50ms）。
const (
	argonTime    uint32 = 3
	argonMemory  uint32 = 64 * 1024 // 64 MiB
	argonThreads uint8  = 4
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

var ErrPasswordMismatch = errors.New("密码不匹配")

// HashPassword 用 argon2id 哈希密码。返回 PHC 编码字符串。
func HashPassword(plain string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("生成 salt: %w", err)
	}
	key := argon2.IDKey([]byte(plain), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory, argonTime, argonThreads,
		b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword 用 ConstantTimeCompare 检查密码是否匹配 hash。
func VerifyPassword(plain, encoded string) error {
	memory, time, threads, salt, expectedKey, err := parsePHC(encoded)
	if err != nil {
		return err
	}
	actualKey := argon2.IDKey([]byte(plain), salt, time, memory, threads, uint32(len(expectedKey)))
	if subtle.ConstantTimeCompare(actualKey, expectedKey) == 1 {
		return nil
	}
	return ErrPasswordMismatch
}

func parsePHC(s string) (memory, time uint32, threads uint8, salt, key []byte, err error) {
	parts := strings.Split(s, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		err = fmt.Errorf("非法的 argon2id PHC 字符串")
		return
	}
	params := strings.Split(parts[3], ",")
	if len(params) != 3 {
		err = fmt.Errorf("argon2id 参数段格式错误: %q", parts[3])
		return
	}
	var m, t, p int
	if _, e := fmt.Sscanf(params[0], "m=%d", &m); e != nil {
		err = fmt.Errorf("argon2id m: %w", e)
		return
	}
	if _, e := fmt.Sscanf(params[1], "t=%d", &t); e != nil {
		err = fmt.Errorf("argon2id t: %w", e)
		return
	}
	if _, e := fmt.Sscanf(params[2], "p=%d", &p); e != nil {
		err = fmt.Errorf("argon2id p: %w", e)
		return
	}
	memory = uint32(m)
	time = uint32(t)
	threads = uint8(p)
	b64 := base64.RawStdEncoding
	if salt, err = b64.DecodeString(parts[4]); err != nil {
		err = fmt.Errorf("解码 salt: %w", err)
		return
	}
	if key, err = b64.DecodeString(parts[5]); err != nil {
		err = fmt.Errorf("解码 hash: %w", err)
		return
	}
	return
}

// NewSessionToken 生成登录后的 session bearer。
// 形态：sess_<64 字节 base64url 无填充>。前缀让日志/排错时一眼能认出。
func NewSessionToken() (string, error) {
	buf := make([]byte, 48)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成 session token: %w", err)
	}
	return "sess_" + base64.RawURLEncoding.EncodeToString(buf), nil
}
