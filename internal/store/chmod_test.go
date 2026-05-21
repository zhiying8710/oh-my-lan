package store

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOpen_FilePermissionsAre600(t *testing.T) {
	if runtime.GOOS == "windows" { t.Skip("POSIX 权限测试跳过 Windows") }
	dir := t.TempDir()
	path := filepath.Join(dir, "x.db")
	s, err := Open(context.Background(), path)
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { _ = s.Close() })
	info, err := os.Stat(path)
	if err != nil { t.Fatal(err) }
	if info.Mode().Perm() != 0o600 {
		t.Errorf("权限应为 0600, got %o", info.Mode().Perm())
	}
}
