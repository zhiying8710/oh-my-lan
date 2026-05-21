package server

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadOrCreateKeySeed_ExplicitWins(t *testing.T) {
	dir := t.TempDir()
	got, err := loadOrCreateKeySeed(dir, "explicit-seed")
	if err != nil {
		t.Fatal(err)
	}
	if got != "explicit-seed" {
		t.Errorf("explicit 应优先, got %q", got)
	}
	// 不应该写盘
	if _, err := os.Stat(filepath.Join(dir, "chisel.seed")); !os.IsNotExist(err) {
		t.Errorf("explicit 模式不应落盘, 但文件存在: %v", err)
	}
}

func TestLoadOrCreateKeySeed_GeneratesAndPersists(t *testing.T) {
	dir := t.TempDir()
	first, err := loadOrCreateKeySeed(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if first == "" {
		t.Fatal("空 seed")
	}

	// 第二次应该读到同一个值
	second, err := loadOrCreateKeySeed(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Errorf("第二次应复用同一 seed, got %q vs %q", second, first)
	}

	// 文件权限应为 0o600（POSIX 平台）
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, "chisel.seed"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("权限应为 0600, got %o", info.Mode().Perm())
		}
	}
}
