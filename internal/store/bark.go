package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// BarkSettings 是单行配置：bark 推送的开关、URL、离线阈值。
// id 在 SQL 层约束 = 1，store API 不暴露这个细节。
type BarkSettings struct {
	Enabled                 bool
	BarkURL                 string
	OfflineThresholdSeconds int
	UpdatedAt               time.Time
}

// GetBarkSettings 返回当前 bark 配置。从未配置过返回 zero value + 无 error，方便上层判断
// "是否启用"用 Enabled 字段——避免引入 ErrNotFound 让 handler 分支变多。
func (s *Store) GetBarkSettings(ctx context.Context) (BarkSettings, error) {
	var bs BarkSettings
	row := s.db.QueryRowContext(ctx, `
		SELECT enabled, bark_url, offline_threshold_seconds, updated_at
		FROM bark_settings WHERE id = 1`)
	var enabled int
	var ts string
	err := row.Scan(&enabled, &bs.BarkURL, &bs.OfflineThresholdSeconds, &ts)
	if errors.Is(err, sql.ErrNoRows) {
		// 默认未启用、空 URL、180s 阈值
		bs.OfflineThresholdSeconds = 180
		return bs, nil
	}
	if err != nil {
		return bs, err
	}
	bs.Enabled = enabled == 1
	bs.UpdatedAt, _ = time.Parse(time.RFC3339, ts)
	return bs, nil
}

// SetBarkSettings 覆盖写当前 bark 配置（upsert）。enabled=false 时其它字段也保留——
// 方便用户暂时关推送但保留 URL，下次重新打开不用重填。
func (s *Store) SetBarkSettings(ctx context.Context, bs BarkSettings) error {
	enabled := 0
	if bs.Enabled {
		enabled = 1
	}
	if bs.OfflineThresholdSeconds <= 0 {
		bs.OfflineThresholdSeconds = 180
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO bark_settings (id, enabled, bark_url, offline_threshold_seconds, updated_at)
		VALUES (1, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		    enabled = excluded.enabled,
		    bark_url = excluded.bark_url,
		    offline_threshold_seconds = excluded.offline_threshold_seconds,
		    updated_at = excluded.updated_at`,
		enabled, bs.BarkURL, bs.OfflineThresholdSeconds, now)
	return err
}

// MarkDeviceAlerted 记录一个 device 已经被 alert 过，防止 reaper 反复推送相同状态。
// 设备 online 时调 ClearDeviceAlert 清掉，下次掉线才会再推。
func (s *Store) MarkDeviceAlerted(ctx context.Context, deviceID string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO device_alert_state (device_id, alerted_at)
		VALUES (?, ?)
		ON CONFLICT(device_id) DO UPDATE SET alerted_at = excluded.alerted_at`,
		deviceID, time.Now().UTC().Format(time.RFC3339))
	return err
}

func (s *Store) ClearDeviceAlert(ctx context.Context, deviceID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM device_alert_state WHERE device_id = ?`, deviceID)
	return err
}

func (s *Store) IsDeviceAlerted(ctx context.Context, deviceID string) (bool, error) {
	var dummy string
	err := s.db.QueryRowContext(ctx,
		`SELECT device_id FROM device_alert_state WHERE device_id = ?`, deviceID).Scan(&dummy)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
