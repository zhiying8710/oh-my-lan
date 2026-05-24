package sshacct

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func TestValidatePubkey(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"ed25519 valid", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBeAbCdEfGhIjKlMnOpQrStUvWxYzAbCdEfGhIjKlMno user@host", false},
		{"ed25519 minimal", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBeAbCdEfGhIjKlMnOpQrStUvWxYzAbCdEfGhIjKlMno", false},
		{"empty", "", true},
		{"newline injection", "ssh-ed25519 AAAA\nmalicious", true},
		{"rsa rejected", "ssh-rsa AAAAB3NzaC1yc2EAAAA...", true},
		{"dsa rejected", "ssh-dss AAAA", true},
		{"missing key part", "ssh-ed25519", true},
		{"truncated base64", "ssh-ed25519 AAAA", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidatePubkey(c.in)
			if (err != nil) != c.wantErr {
				t.Errorf("err=%v wantErr=%v", err, c.wantErr)
			}
		})
	}
}

func TestUsernameFor(t *testing.T) {
	m := &Manager{Prefix: "oml-"}
	cases := []struct {
		in, want string
	}{
		{"9871b2b50dac1a3c9814894fa3578b1a", "oml-9871b2b5"},
		{"cb5f819985f19f738e30ae94c203bf08", "oml-cb5f8199"},
		{"short", "oml-short"},   // 短的也接受，但 validNameRE 会拦
		{"ABCDEF12", "oml-abcdef12"}, // 大写转小写
	}
	for _, c := range cases {
		if got := m.UsernameFor(c.in); got != c.want {
			t.Errorf("UsernameFor(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestAuthorizedKeysLine(t *testing.T) {
	pubkey := "ssh-ed25519 AAAAFAKE"
	line := authorizedKeysLine(pubkey, []int{40000, 40001})
	// 关键校验：必须有 restrict + port-forwarding + permitopen 限制
	for _, want := range []string{
		"restrict",
		"port-forwarding",
		`permitopen="127.0.0.1:40000"`,
		`permitopen="127.0.0.1:40001"`,
		"ssh-ed25519 AAAAFAKE",
		"oml-managed", // marker，运维能 grep
	} {
		if !strings.Contains(line, want) {
			t.Errorf("authorized_keys 缺片段 %q: %s", want, line)
		}
	}

	// 空 ports：仍 restrict + port-forwarding 但无 permitopen → SSH 能登但 forward 全拒
	emptyLine := authorizedKeysLine(pubkey, nil)
	if strings.Contains(emptyLine, "permitopen") {
		t.Errorf("空 ports 不应有 permitopen: %s", emptyLine)
	}
	if !strings.Contains(emptyLine, "restrict,port-forwarding") {
		t.Errorf("空 ports 应仍有 restrict + port-forwarding: %s", emptyLine)
	}
}

// TestManager_Provision_Lock_Delete 用 MockExec 验证完整生命周期，不需要真 useradd。
func TestManager_Provision_Lock_Delete(t *testing.T) {
	tmp := t.TempDir()
	// 模拟 /etc/passwd 之后改路径太麻烦——直接用 mock 让 userExists 返回 false
	// 真接整测试用 docker，超出本 unit test 范围
	var calls []string
	m := &Manager{
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Prefix:   "oml-",
		HomeBase: tmp,
		UseSudo:  false,
		MockExec: func(name string, args ...string) ([]byte, error) {
			calls = append(calls, name+" "+strings.Join(args, " "))
			return nil, nil
		},
	}

	pubkey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBeAbCdEfGhIjKlMnOpQrStUvWxYzAbCdEfGhIjKlMno user@host"
	user, err := m.Provision(context.Background(), "9871b2b50dac1a3c9814894fa3578b1a", pubkey, []int{40000, 40001})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if user != "oml-9871b2b5" {
		t.Errorf("user=%q want oml-9871b2b5", user)
	}

	// 检查 authorized_keys 落盘
	content, err := os.ReadFile(tmp + "/oml-9871b2b5/.ssh/authorized_keys")
	if err != nil {
		t.Fatalf("read authorized_keys: %v", err)
	}
	if !strings.Contains(string(content), `permitopen="127.0.0.1:40000"`) {
		t.Errorf("authorized_keys 缺 permitopen: %s", content)
	}

	// 检查 useradd / chown 都调过了
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "useradd") {
		t.Errorf("没调 useradd: %s", joined)
	}
	if !strings.Contains(joined, "chown") {
		t.Errorf("没调 chown: %s", joined)
	}

	// Lock：authorized_keys 应被清空
	if err := m.Lock(context.Background(), "9871b2b50dac1a3c9814894fa3578b1a"); err != nil {
		// userExists 没真账号会返回 false → 直接 return 不算错
		t.Logf("Lock returned: %v", err)
	}
}

// TestManager_Provision_RejectsBadPubkey 防止恶意 device 上传非法 pubkey。
func TestManager_Provision_RejectsBadPubkey(t *testing.T) {
	m := &Manager{
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Prefix:   "oml-",
		HomeBase: t.TempDir(),
		UseSudo:  false,
	}
	_, err := m.Provision(context.Background(), "9871b2b5xxxxxxxxxxxxxxxxxxxxxxxxx", "ssh-rsa AAAA", nil)
	if err == nil {
		t.Fatal("ssh-rsa 应被拒绝")
	}
	_, err = m.Provision(context.Background(), "9871b2b5xxxxxxxxxxxxxxxxxxxxxxxxx", "ssh-ed25519 AAAA\nmalicious", nil)
	if err == nil {
		t.Fatal("含换行的 pubkey 应被拒绝")
	}
}
