package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Device struct {
	ID               string
	Name             string
	TunnelSecret string
	Status           string
	CreatedAt        time.Time
	LastSeenAt       *time.Time
}

const (
	DeviceStatusOffline = "offline"
	DeviceStatusOnline  = "online"
)

var ErrNotFound = errors.New("记录不存在")

// CreateDevice 插入设备记录。调用方负责生成 ID 与 hash。
func (s *Store) CreateDevice(ctx context.Context, d Device) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO devices(id, name, tunnel_secret, status, created_at)
		VALUES(?,?,?,?,?)`,
		d.ID, d.Name, d.TunnelSecret, statusOrDefault(d.Status), d.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("插入 device: %w", err)
	}
	return nil
}

func statusOrDefault(s string) string {
	if s == "" {
		return DeviceStatusOffline
	}
	return s
}

func (s *Store) GetDeviceByID(ctx context.Context, id string) (Device, error) {
	return s.scanDevice(ctx,
		`SELECT id, name, tunnel_secret, status, created_at, last_seen_at FROM devices WHERE id = ?`, id)
}

func (s *Store) GetDeviceByName(ctx context.Context, name string) (Device, error) {
	return s.scanDevice(ctx,
		`SELECT id, name, tunnel_secret, status, created_at, last_seen_at FROM devices WHERE name = ?`, name)
}

func (s *Store) ListDevices(ctx context.Context) ([]Device, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, tunnel_secret, status, created_at, last_seen_at FROM devices ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("列设备: %w", err)
	}
	defer rows.Close()

	var out []Device
	for rows.Next() {
		d, err := scanDeviceRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) UpdateDeviceStatus(ctx context.Context, id, status string, lastSeen time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE devices SET status=?, last_seen_at=? WHERE id=?`,
		status, lastSeen.UTC().Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return fmt.Errorf("更新 device 状态: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkStaleDevicesOffline 把 last_seen_at 早于 before 的在线设备批量标为 offline。
// 返回标记的行数。从未上报过 last_seen_at（NULL）但状态仍为 online 的设备也算 stale。
func (s *Store) MarkStaleDevicesOffline(ctx context.Context, before time.Time) (int, error) {
	cutoff := before.UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `
		UPDATE devices
		SET status = ?
		WHERE status = ?
		  AND (last_seen_at IS NULL OR last_seen_at < ?)`,
		DeviceStatusOffline, DeviceStatusOnline, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("批量下线 stale device: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// DeviceWithCounts 是 device 行 + 子查询 services_count / forwards_count 的扁平视图，
// 用于 admin/devices 列表避免 N+1。
type DeviceWithCounts struct {
	Device
	ServicesCount int
	ForwardsCount int
}

// ListDevicesWithCounts 一次 SQL 拿全设备 + 关联计数。
func (s *Store) ListDevicesWithCounts(ctx context.Context) ([]DeviceWithCounts, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			d.id, d.name, d.tunnel_secret, d.status, d.created_at, d.last_seen_at,
			(SELECT COUNT(*) FROM services WHERE device_id = d.id)       AS svc_count,
			(SELECT COUNT(*) FROM forwards WHERE owner_device_id = d.id) AS fwd_count
		FROM devices d
		ORDER BY d.created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("查询 devices with counts: %w", err)
	}
	defer rows.Close()

	var out []DeviceWithCounts
	for rows.Next() {
		var (
			it         DeviceWithCounts
			createdAt  string
			lastSeenAt sql.NullString
		)
		if err := rows.Scan(&it.ID, &it.Name, &it.TunnelSecret, &it.Status, &createdAt, &lastSeenAt,
			&it.ServicesCount, &it.ForwardsCount); err != nil {
			return nil, fmt.Errorf("扫描 device join 行: %w", err)
		}
		t, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, fmt.Errorf("解析 created_at: %w", err)
		}
		it.CreatedAt = t
		if lastSeenAt.Valid {
			ls, err := time.Parse(time.RFC3339Nano, lastSeenAt.String)
			if err != nil {
				return nil, fmt.Errorf("解析 last_seen_at: %w", err)
			}
			it.LastSeenAt = &ls
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// DeleteDevice 删除设备，触发级联删除 services 与 enrollment_tokens.used_by_device_id 置空。
func (s *Store) DeleteDevice(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM devices WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除 device: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) scanDevice(ctx context.Context, query string, args ...any) (Device, error) {
	row := s.db.QueryRowContext(ctx, query, args...)
	var (
		d            Device
		createdAt    string
		lastSeenAt   sql.NullString
	)
	if err := row.Scan(&d.ID, &d.Name, &d.TunnelSecret, &d.Status, &createdAt, &lastSeenAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return d, ErrNotFound
		}
		return d, fmt.Errorf("读 device: %w", err)
	}
	t, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return d, fmt.Errorf("解析 created_at: %w", err)
	}
	d.CreatedAt = t
	if lastSeenAt.Valid {
		ls, err := time.Parse(time.RFC3339Nano, lastSeenAt.String)
		if err != nil {
			return d, fmt.Errorf("解析 last_seen_at: %w", err)
		}
		d.LastSeenAt = &ls
	}
	return d, nil
}

func scanDeviceRow(rows *sql.Rows) (Device, error) {
	var (
		d          Device
		createdAt  string
		lastSeenAt sql.NullString
	)
	if err := rows.Scan(&d.ID, &d.Name, &d.TunnelSecret, &d.Status, &createdAt, &lastSeenAt); err != nil {
		return d, fmt.Errorf("扫描 device 行: %w", err)
	}
	t, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return d, fmt.Errorf("解析 created_at: %w", err)
	}
	d.CreatedAt = t
	if lastSeenAt.Valid {
		ls, err := time.Parse(time.RFC3339Nano, lastSeenAt.String)
		if err != nil {
			return d, fmt.Errorf("解析 last_seen_at: %w", err)
		}
		d.LastSeenAt = &ls
	}
	return d, nil
}
