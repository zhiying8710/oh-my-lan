// Package store 封装 oh-my-lan 服务端的 SQLite 持久化。
// 不依赖 CGO，使用 modernc.org/sqlite 纯 Go 驱动。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	sqlite "modernc.org/sqlite"
	sqlitelib "modernc.org/sqlite/lib"
)

// Store 是 SQLite 连接池的薄封装。所有 CRUD 方法都通过 receiver 方法暴露。
type Store struct {
	db *sql.DB
}

// Open 打开 SQLite 数据库，启用外键，自动跑迁移。
// path 是 sqlite 文件路径；":memory:" 表示内存库（仅供测试）。
//
// 对落盘 DB 强制 0o600 权限：本表包含明文 tunnel_secret（功能等价于 SSH 私钥），
// 必须按"机密文件"级别保护。
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := buildDSN(path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开 SQLite (%s): %w", path, err)
	}
	// modernc 的 sqlite 驱动不是真正并发，单连接最安全。
	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping SQLite: %w", err)
	}
	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("迁移失败: %w", err)
	}
	if path != ":memory:" {
		if err := os.Chmod(path, 0o600); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("收紧 DB 权限 %s: %w", path, err)
		}
	}
	return &Store{db: db}, nil
}

func buildDSN(path string) string {
	if path == ":memory:" {
		return ":memory:?_pragma=foreign_keys(1)"
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	v := url.Values{}
	v.Add("_pragma", "foreign_keys(1)")
	v.Add("_pragma", "journal_mode(WAL)")
	v.Add("_pragma", "busy_timeout(5000)")
	return "file:" + abs + "?" + v.Encode()
}

func (s *Store) Close() error { return s.db.Close() }

// DB 暴露底层句柄，仅供同包测试使用。
func (s *Store) DB() *sql.DB { return s.db }

// IsUniqueViolation 判断 err 是否由 SQLite UNIQUE / PRIMARY KEY 约束触发。
//
// 优先用 modernc.org/sqlite 的错误码（类型安全）；
// 兼容性 fallback 用字符串匹配，避免日后驱动包升级 API 变更时回归。
func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var sErr *sqlite.Error
	if errors.As(err, &sErr) {
		code := sErr.Code()
		return code == sqlitelib.SQLITE_CONSTRAINT_UNIQUE ||
			code == sqlitelib.SQLITE_CONSTRAINT_PRIMARYKEY
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "constraint failed: UNIQUE")
}
