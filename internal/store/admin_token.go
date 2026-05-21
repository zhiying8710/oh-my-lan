package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type AdminToken struct {
	ID         string
	TokenHash  string
	Label      string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

func (s *Store) CreateAdminToken(ctx context.Context, t AdminToken) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO admin_tokens(id, token_hash, label, created_at)
		VALUES(?,?,?,?)`,
		t.ID, t.TokenHash, t.Label,
		t.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("插入 admin_token: %w", err)
	}
	return nil
}

func (s *Store) GetAdminTokenByHash(ctx context.Context, hash string) (AdminToken, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, token_hash, label, created_at, last_used_at
		FROM admin_tokens WHERE token_hash = ?`, hash)
	return scanAdminToken(row)
}

func (s *Store) ListAdminTokens(ctx context.Context) ([]AdminToken, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, token_hash, label, created_at, last_used_at
		FROM admin_tokens ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("列 admin_token: %w", err)
	}
	defer rows.Close()
	var out []AdminToken
	for rows.Next() {
		t, err := scanAdminTokenRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) DeleteAdminToken(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM admin_tokens WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删 admin_token: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchAdminToken 更新 last_used_at；失败仅记录不阻断认证。
func (s *Store) TouchAdminToken(ctx context.Context, id string, ts time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE admin_tokens SET last_used_at = ? WHERE id = ?`,
		ts.UTC().Format(time.RFC3339Nano), id)
	return err
}

func scanAdminToken(row *sql.Row) (AdminToken, error) {
	var (
		t        AdminToken
		created  string
		lastUsed sql.NullString
	)
	if err := row.Scan(&t.ID, &t.TokenHash, &t.Label, &created, &lastUsed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return t, ErrNotFound
		}
		return t, fmt.Errorf("读 admin_token: %w", err)
	}
	c, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return t, fmt.Errorf("解析 created_at: %w", err)
	}
	t.CreatedAt = c
	if lastUsed.Valid {
		lu, err := time.Parse(time.RFC3339Nano, lastUsed.String)
		if err != nil {
			return t, fmt.Errorf("解析 last_used_at: %w", err)
		}
		t.LastUsedAt = &lu
	}
	return t, nil
}

func scanAdminTokenRow(rows *sql.Rows) (AdminToken, error) {
	var (
		t        AdminToken
		created  string
		lastUsed sql.NullString
	)
	if err := rows.Scan(&t.ID, &t.TokenHash, &t.Label, &created, &lastUsed); err != nil {
		return t, fmt.Errorf("扫描 admin_token 行: %w", err)
	}
	c, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return t, fmt.Errorf("解析 created_at: %w", err)
	}
	t.CreatedAt = c
	if lastUsed.Valid {
		lu, err := time.Parse(time.RFC3339Nano, lastUsed.String)
		if err != nil {
			return t, fmt.Errorf("解析 last_used_at: %w", err)
		}
		t.LastUsedAt = &lu
	}
	return t, nil
}
