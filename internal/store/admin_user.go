package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type AdminUser struct {
	ID           string
	Username     string
	PasswordHash string // argon2id PHC 字符串
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (s *Store) CreateAdminUser(ctx context.Context, u AdminUser) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO admin_users(id, username, password_hash, created_at, updated_at)
		VALUES(?,?,?,?,?)`,
		u.ID, u.Username, u.PasswordHash,
		u.CreatedAt.UTC().Format(time.RFC3339Nano),
		u.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("插入 admin_user: %w", err)
	}
	return nil
}

func (s *Store) GetAdminUserByUsername(ctx context.Context, username string) (AdminUser, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, created_at, updated_at
		FROM admin_users WHERE username = ?`, username)
	return scanAdminUser(row)
}

func (s *Store) GetAdminUserByID(ctx context.Context, id string) (AdminUser, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, created_at, updated_at
		FROM admin_users WHERE id = ?`, id)
	return scanAdminUser(row)
}

func (s *Store) ListAdminUsers(ctx context.Context) ([]AdminUser, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, username, password_hash, created_at, updated_at
		FROM admin_users ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("列 admin_user: %w", err)
	}
	defer rows.Close()
	var out []AdminUser
	for rows.Next() {
		var (
			u             AdminUser
			created, upd  string
		)
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &created, &upd); err != nil {
			return nil, fmt.Errorf("扫描 admin_user 行: %w", err)
		}
		t1, e1 := time.Parse(time.RFC3339Nano, created)
		t2, e2 := time.Parse(time.RFC3339Nano, upd)
		if e1 != nil || e2 != nil {
			return nil, fmt.Errorf("解析时间戳: %v / %v", e1, e2)
		}
		u.CreatedAt, u.UpdatedAt = t1, t2
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdateAdminUserPassword 改密码 + 更新 updated_at。
func (s *Store) UpdateAdminUserPassword(ctx context.Context, username, newHash string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE admin_users SET password_hash = ?, updated_at = ? WHERE username = ?`,
		newHash, time.Now().UTC().Format(time.RFC3339Nano), username)
	if err != nil {
		return fmt.Errorf("更新 admin_user 密码: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteAdminUser(ctx context.Context, username string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM admin_users WHERE username = ?`, username)
	if err != nil {
		return fmt.Errorf("删 admin_user: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountAdminUsers 用于服务启动时判断是否需要 setup 引导。
func (s *Store) CountAdminUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_users`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count admin_users: %w", err)
	}
	return n, nil
}

func scanAdminUser(row *sql.Row) (AdminUser, error) {
	var (
		u            AdminUser
		created, upd string
	)
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &created, &upd); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return u, ErrNotFound
		}
		return u, fmt.Errorf("读 admin_user: %w", err)
	}
	t1, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return u, fmt.Errorf("解析 created_at: %w", err)
	}
	t2, err := time.Parse(time.RFC3339Nano, upd)
	if err != nil {
		return u, fmt.Errorf("解析 updated_at: %w", err)
	}
	u.CreatedAt, u.UpdatedAt = t1, t2
	return u, nil
}
