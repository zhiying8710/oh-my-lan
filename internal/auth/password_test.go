package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestHashAndVerifyPassword_RoundTrip(t *testing.T) {
	enc, err := HashPassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=19$") {
		t.Errorf("PHC 前缀不对: %s", enc)
	}
	if err := VerifyPassword("hunter2", enc); err != nil {
		t.Errorf("正确密码应通过: %v", err)
	}
	if err := VerifyPassword("wrong", enc); !errors.Is(err, ErrPasswordMismatch) {
		t.Errorf("错误密码应 ErrPasswordMismatch, got %v", err)
	}
}

func TestVerifyPassword_RejectsMalformed(t *testing.T) {
	cases := []string{
		"plain-text",
		"$argon2id$v=19$m=64$x$y",
		"$bcrypt$xxx",
	}
	for _, c := range cases {
		if err := VerifyPassword("any", c); err == nil || errors.Is(err, ErrPasswordMismatch) {
			t.Errorf("应拒非法 PHC %q, got %v", c, err)
		}
	}
}

func TestNewSessionToken_FormatAndUnique(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 10; i++ {
		tk, err := NewSessionToken()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(tk, "sess_") {
			t.Errorf("session token 缺前缀: %s", tk)
		}
		if _, dup := seen[tk]; dup {
			t.Errorf("token 重复: %s", tk)
		}
		seen[tk] = struct{}{}
	}
}
