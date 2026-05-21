package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Session struct {
	ID         string
	UserID     string
	TokenHash  string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastUsedAt *time.Time
}

func (s *Store) CreateSession(ctx context.Context, sess Session) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions(id, user_id, token_hash, created_at, expires_at)
		VALUES(?,?,?,?,?)`,
		sess.ID, sess.UserID, sess.TokenHash,
		sess.CreatedAt.UTC().Format(time.RFC3339Nano),
		sess.ExpiresAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("插入 session: %w", err)
	}
	return nil
}

// GetActiveSessionByHash 返回未过期的 session；过期或不存在都返回 ErrNotFound。
// 调用方应在校验通过后调 TouchSession 更新 last_used_at。
func (s *Store) GetActiveSessionByHash(ctx context.Context, hash string, now time.Time) (Session, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, token_hash, created_at, expires_at, last_used_at
		FROM sessions WHERE token_hash = ? AND expires_at > ?`,
		hash, now.UTC().Format(time.RFC3339Nano))
	return scanSession(row)
}

func (s *Store) TouchSession(ctx context.Context, id string, ts time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET last_used_at = ? WHERE id = ?`,
		ts.UTC().Format(time.RFC3339Nano), id)
	return err
}

func (s *Store) DeleteSessionByHash(ctx context.Context, hash string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hash)
	if err != nil {
		return fmt.Errorf("删 session: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteSessionsByUserID(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	if err != nil {
		return fmt.Errorf("删 user 的全部 session: %w", err)
	}
	return nil
}

// DeleteExpiredSessions 清掉所有 expires_at <= now 的行；返回清掉的条数。
// 由后台 reaper 周期调用。
func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at <= ?`,
		now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("清过期 session: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func scanSession(row *sql.Row) (Session, error) {
	var (
		sess              Session
		created, expires  string
		lastUsed          sql.NullString
	)
	if err := row.Scan(&sess.ID, &sess.UserID, &sess.TokenHash, &created, &expires, &lastUsed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sess, ErrNotFound
		}
		return sess, fmt.Errorf("读 session: %w", err)
	}
	c, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return sess, fmt.Errorf("解析 created_at: %w", err)
	}
	e, err := time.Parse(time.RFC3339Nano, expires)
	if err != nil {
		return sess, fmt.Errorf("解析 expires_at: %w", err)
	}
	sess.CreatedAt = c
	sess.ExpiresAt = e
	if lastUsed.Valid {
		lu, err := time.Parse(time.RFC3339Nano, lastUsed.String)
		if err != nil {
			return sess, fmt.Errorf("解析 last_used_at: %w", err)
		}
		sess.LastUsedAt = &lu
	}
	return sess, nil
}
