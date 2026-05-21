package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// AuditEntry 是 audit_log 一行的 Go 视图。
type AuditEntry struct {
	ID     string
	TS     time.Time
	Actor  string // 'admin:<token_id>' / 'device:<device_id>' / 'system'
	Action string // e.g. 'device.enroll'
	Target string // 实体 id（可空）
	Detail string // JSON
}

// WriteAudit 落一条审计记录。
// 调用方应保证传入的 entry.TS 已是 UTC；ID 留空时由本方法生成（用 ts+rand）。
// 失败不应阻断业务路径——调用方通常 log warning 后忽略。
func (s *Store) WriteAudit(ctx context.Context, entry AuditEntry) error {
	if entry.ID == "" {
		return fmt.Errorf("audit entry id 必填")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO audit_log(id, ts, actor, action, target, detail)
		VALUES(?,?,?,?,?,?)`,
		entry.ID,
		entry.TS.UTC().Format(time.RFC3339Nano),
		entry.Actor, entry.Action,
		nullable(entry.Target), nullable(entry.Detail),
	)
	if err != nil {
		return fmt.Errorf("写 audit_log: %w", err)
	}
	return nil
}

// ListAuditRecent 按 ts 倒序返回最近 limit 条（limit<=0 时默认 200，上限 1000）。
func (s *Store) ListAuditRecent(ctx context.Context, limit int) ([]AuditEntry, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, ts, actor, action, target, detail
		FROM audit_log ORDER BY ts DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("列 audit_log: %w", err)
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var (
			e      AuditEntry
			ts     string
			tgt    sql.NullString
			det    sql.NullString
		)
		if err := rows.Scan(&e.ID, &ts, &e.Actor, &e.Action, &tgt, &det); err != nil {
			return nil, fmt.Errorf("扫 audit 行: %w", err)
		}
		t, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil {
			return nil, fmt.Errorf("解析 audit.ts: %w", err)
		}
		e.TS = t
		if tgt.Valid {
			e.Target = tgt.String
		}
		if det.Valid {
			e.Detail = det.String
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
