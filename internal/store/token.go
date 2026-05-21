package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type EnrollmentToken struct {
	ID             string
	TokenHash      string
	ExpiresAt      time.Time
	UsedAt         *time.Time
	UsedByDeviceID *string
	CreatedAt      time.Time
}

func (s *Store) CreateEnrollmentToken(ctx context.Context, t EnrollmentToken) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO enrollment_tokens(id, token_hash, expires_at, created_at)
		VALUES(?,?,?,?)`,
		t.ID, t.TokenHash,
		t.ExpiresAt.UTC().Format(time.RFC3339Nano),
		t.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("插入 enrollment_token: %w", err)
	}
	return nil
}

func (s *Store) GetEnrollmentTokenByHash(ctx context.Context, hash string) (EnrollmentToken, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, token_hash, expires_at, used_at, used_by_device_id, created_at
		FROM enrollment_tokens WHERE token_hash = ?`, hash)
	return scanToken(row)
}

// ConsumeEnrollmentToken 在事务内把 token 标记为已用，并写入对应 device。
// 调用方保证 device.TunnelSecret 等已就绪；token 未过期由调用方校验。
// 如果 token 已被使用或不存在，返回 ErrNotFound（保持时序攻击友好的统一返回）。
func (s *Store) ConsumeEnrollmentToken(ctx context.Context, tokenHash string, dev Device) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启事务: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, `
		SELECT id, used_at FROM enrollment_tokens WHERE token_hash = ?`, tokenHash)
	var tokenID string
	var usedAt sql.NullString
	if err := row.Scan(&tokenID, &usedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("查询 token: %w", err)
	}
	if usedAt.Valid {
		return ErrNotFound
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO devices(id, name, tunnel_secret, status, created_at)
		VALUES(?,?,?,?,?)`,
		dev.ID, dev.Name, dev.TunnelSecret, statusOrDefault(dev.Status),
		dev.CreatedAt.UTC().Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("插入 device: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
		UPDATE enrollment_tokens SET used_at = ?, used_by_device_id = ? WHERE id = ?`,
		now, dev.ID, tokenID,
	); err != nil {
		return fmt.Errorf("标记 token 已用: %w", err)
	}
	return tx.Commit()
}

func scanToken(row *sql.Row) (EnrollmentToken, error) {
	var (
		t          EnrollmentToken
		expires    string
		created    string
		usedAt     sql.NullString
		usedByDev  sql.NullString
	)
	if err := row.Scan(&t.ID, &t.TokenHash, &expires, &usedAt, &usedByDev, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return t, ErrNotFound
		}
		return t, fmt.Errorf("读 token: %w", err)
	}
	e, err := time.Parse(time.RFC3339Nano, expires)
	if err != nil {
		return t, fmt.Errorf("解析 expires_at: %w", err)
	}
	c, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return t, fmt.Errorf("解析 created_at: %w", err)
	}
	t.ExpiresAt = e
	t.CreatedAt = c
	if usedAt.Valid {
		ts, err := time.Parse(time.RFC3339Nano, usedAt.String)
		if err != nil {
			return t, fmt.Errorf("解析 used_at: %w", err)
		}
		t.UsedAt = &ts
	}
	if usedByDev.Valid {
		v := usedByDev.String
		t.UsedByDeviceID = &v
	}
	return t, nil
}
