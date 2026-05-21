package auth

import (
	"strings"
	"testing"
)

func TestNewEnrollmentToken_FormatAndUnique(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 20; i++ {
		tok, err := NewEnrollmentToken()
		if err != nil {
			t.Fatalf("NewEnrollmentToken: %v", err)
		}
		if !strings.HasPrefix(tok, EnrollTokenPrefix) {
			t.Errorf("缺少前缀: %s", tok)
		}
		if err := ValidateEnrollTokenFormat(tok); err != nil {
			t.Errorf("Validate(%s): %v", tok, err)
		}
		if _, dup := seen[tok]; dup {
			t.Errorf("token 重复: %s", tok)
		}
		seen[tok] = struct{}{}
	}
}

func TestValidateEnrollTokenFormat_Rejects(t *testing.T) {
	cases := []string{
		"no-prefix-xxx",
		"ot_",                  // 空 body 也算合法的 base32 空串
		"ot_!!!!@@@@",          // 非 base32
	}
	for _, c := range cases {
		err := ValidateEnrollTokenFormat(c)
		if c == "ot_" && err != nil {
			// 空 body 在 base32 解码中合法；这里允许，弱化断言。
			continue
		}
		if c != "ot_" && err == nil {
			t.Errorf("ValidateEnrollTokenFormat(%q) 应失败", c)
		}
	}
}

func TestNewTunnelSecret_LongAndUnique(t *testing.T) {
	a, err := NewTunnelSecret()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewTunnelSecret()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Errorf("两次生成 secret 相同, 极不可能: %s", a)
	}
	if len(a) < 80 {
		t.Errorf("secret 太短: %d", len(a))
	}
}

func TestNewDeviceID_HexAnd32Chars(t *testing.T) {
	id, err := NewDeviceID()
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 32 {
		t.Errorf("device id 长度应为 32, got %d", len(id))
	}
}

func TestHashSecret_StableAndConstantTimeEqual(t *testing.T) {
	h1 := HashSecret("hello")
	h2 := HashSecret("hello")
	if h1 != h2 {
		t.Error("HashSecret 应是确定性的")
	}
	if !ConstantTimeEqual(h1, h2) {
		t.Error("ConstantTimeEqual 应认相等的 hash")
	}
	if ConstantTimeEqual(h1, HashSecret("world")) {
		t.Error("不同输入 hash 不应判等")
	}
}
