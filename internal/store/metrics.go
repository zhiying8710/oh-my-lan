package store

import (
	"context"
	"fmt"
)

// Counts 是 admin metrics 端点用到的计数集合。
// 一次 SQL 拿全；个人规模下完全够用。
type Counts struct {
	DevicesTotal     int
	DevicesOnline    int
	ServicesTotal    int
	ServicesEnabled  int
	ForwardsTotal    int
	ForwardsEnabled  int
	AdminTokensTotal int
}

// LoadCounts 一次性把所有 metric 计数拉出来。
// SQLite 子查询很便宜，避免应用层多次 round-trip。
func (s *Store) LoadCounts(ctx context.Context) (Counts, error) {
	const q = `
		SELECT
			(SELECT COUNT(*) FROM devices)                                           AS dev_total,
			(SELECT COUNT(*) FROM devices WHERE status = ?)                          AS dev_online,
			(SELECT COUNT(*) FROM services)                                          AS svc_total,
			(SELECT COUNT(*) FROM services WHERE enabled = 1)                        AS svc_enabled,
			(SELECT COUNT(*) FROM forwards)                                          AS fwd_total,
			(SELECT COUNT(*) FROM forwards WHERE enabled = 1)                        AS fwd_enabled,
			(SELECT COUNT(*) FROM admin_tokens)                                      AS at_total`
	var c Counts
	if err := s.db.QueryRowContext(ctx, q, DeviceStatusOnline).Scan(
		&c.DevicesTotal, &c.DevicesOnline,
		&c.ServicesTotal, &c.ServicesEnabled,
		&c.ForwardsTotal, &c.ForwardsEnabled,
		&c.AdminTokensTotal,
	); err != nil {
		return Counts{}, fmt.Errorf("查询 counts: %w", err)
	}
	return c, nil
}
