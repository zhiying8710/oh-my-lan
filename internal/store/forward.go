package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Forward struct {
	ID              string
	OwnerDeviceID   string
	RemoteServiceID string
	LocalPort       int
	Enabled         bool
	CreatedAt       time.Time
}

func (s *Store) CreateForward(ctx context.Context, f Forward) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO forwards(id, owner_device_id, remote_service_id, local_port, enabled, created_at)
		VALUES(?,?,?,?,?,?)`,
		f.ID, f.OwnerDeviceID, f.RemoteServiceID, f.LocalPort,
		boolToInt(f.Enabled), f.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("插入 forward: %w", err)
	}
	return nil
}

func (s *Store) GetForward(ctx context.Context, id string) (Forward, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, owner_device_id, remote_service_id, local_port, enabled, created_at
		FROM forwards WHERE id = ?`, id)
	return scanForward(row)
}

func (s *Store) ListForwardsByOwner(ctx context.Context, ownerDeviceID string) ([]Forward, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, owner_device_id, remote_service_id, local_port, enabled, created_at
		FROM forwards WHERE owner_device_id = ? ORDER BY local_port ASC`, ownerDeviceID)
	if err != nil {
		return nil, fmt.Errorf("列 forwards: %w", err)
	}
	defer rows.Close()
	return collectForwards(rows)
}

// SetForwardEnabled 切换 forward.enabled。daemon 下次 reload 时会拉到最新 bootstrap，
// 自动建立/拆除对应的 L: 隧道。
func (s *Store) SetForwardEnabled(ctx context.Context, id string, enabled bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE forwards SET enabled = ? WHERE id = ?`, boolToInt(enabled), id)
	if err != nil {
		return fmt.Errorf("更新 forward enabled: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ForwardListItem 是 forwards JOIN owner-device + remote-service + remote-device 的扁平视图，
// 一次 SQL 查全所有 handler 渲染 ForwardDTO 需要的字段。
type ForwardListItem struct {
	Forward
	OwnerDeviceName      string
	RemoteServiceName    string
	RemotePublicPort     int
	Protocol             string
	RemoteDeviceID       string
	RemoteDeviceName     string
	RemoteServiceEnabled bool
}

// ListForwardsJoined 返回 forwards × 3 张表的扁平 JOIN。
// ownerID 非空时按 owner 过滤。
//
// 选择 LEFT JOIN 而非 INNER：FK CASCADE 通常会清理，但保险起见——若任一关联缺失，
// 字段以零值返回；调用方据此决定是否跳过该行。
func (s *Store) ListForwardsJoined(ctx context.Context, ownerID string) ([]ForwardListItem, error) {
	const baseSQL = `
		SELECT
			f.id, f.owner_device_id, f.remote_service_id, f.local_port, f.enabled, f.created_at,
			COALESCE(od.name, ''),
			COALESCE(s.name, ''), COALESCE(s.public_port, 0), COALESCE(s.protocol, ''),
			COALESCE(s.device_id, ''), COALESCE(rd.name, ''), COALESCE(s.enabled, 0)
		FROM forwards f
		LEFT JOIN devices  od ON f.owner_device_id   = od.id
		LEFT JOIN services s  ON f.remote_service_id = s.id
		LEFT JOIN devices  rd ON s.device_id         = rd.id`

	var (
		rows *sql.Rows
		err  error
	)
	if ownerID == "" {
		rows, err = s.db.QueryContext(ctx, baseSQL+" ORDER BY f.created_at ASC")
	} else {
		rows, err = s.db.QueryContext(ctx, baseSQL+" WHERE f.owner_device_id = ? ORDER BY f.local_port ASC", ownerID)
	}
	if err != nil {
		return nil, fmt.Errorf("查询 forwards join: %w", err)
	}
	defer rows.Close()

	var out []ForwardListItem
	for rows.Next() {
		var (
			it           ForwardListItem
			enabled      int
			created      string
			remoteEnabled int
		)
		if err := rows.Scan(
			&it.ID, &it.OwnerDeviceID, &it.RemoteServiceID, &it.LocalPort, &enabled, &created,
			&it.OwnerDeviceName,
			&it.RemoteServiceName, &it.RemotePublicPort, &it.Protocol,
			&it.RemoteDeviceID, &it.RemoteDeviceName, &remoteEnabled,
		); err != nil {
			return nil, fmt.Errorf("扫描 forward join 行: %w", err)
		}
		it.Enabled = enabled != 0
		it.RemoteServiceEnabled = remoteEnabled != 0
		c, err := time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, fmt.Errorf("解析 created_at: %w", err)
		}
		it.CreatedAt = c
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) DeleteForward(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM forwards WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删 forward: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanForward(row *sql.Row) (Forward, error) {
	var (
		f       Forward
		enabled int
		created string
	)
	if err := row.Scan(&f.ID, &f.OwnerDeviceID, &f.RemoteServiceID, &f.LocalPort, &enabled, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return f, ErrNotFound
		}
		return f, fmt.Errorf("读 forward: %w", err)
	}
	f.Enabled = enabled != 0
	c, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return f, fmt.Errorf("解析 created_at: %w", err)
	}
	f.CreatedAt = c
	return f, nil
}

func collectForwards(rows *sql.Rows) ([]Forward, error) {
	var out []Forward
	for rows.Next() {
		var (
			f       Forward
			enabled int
			created string
		)
		if err := rows.Scan(&f.ID, &f.OwnerDeviceID, &f.RemoteServiceID, &f.LocalPort, &enabled, &created); err != nil {
			return nil, fmt.Errorf("扫描 forward 行: %w", err)
		}
		f.Enabled = enabled != 0
		c, err := time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, fmt.Errorf("解析 created_at: %w", err)
		}
		f.CreatedAt = c
		out = append(out, f)
	}
	return out, rows.Err()
}
