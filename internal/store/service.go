package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Service struct {
	ID         string
	DeviceID   string
	Name       string
	Protocol   string
	LocalAddr  string
	PublicPort int
	Enabled    bool
	CreatedAt  time.Time
}

const (
	ProtocolTCP = "tcp"
	ProtocolUDP = "udp"
)

func (s *Store) CreateService(ctx context.Context, sv Service) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO services(id, device_id, name, protocol, local_addr, public_port, enabled, created_at)
		VALUES(?,?,?,?,?,?,?,?)`,
		sv.ID, sv.DeviceID, sv.Name, sv.Protocol, sv.LocalAddr,
		sv.PublicPort, boolToInt(sv.Enabled),
		sv.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("插入 service: %w", err)
	}
	return nil
}

func (s *Store) GetService(ctx context.Context, id string) (Service, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, device_id, name, protocol, local_addr, public_port, enabled, created_at
		FROM services WHERE id = ?`, id)
	return scanService(row)
}

func (s *Store) ListServicesByDevice(ctx context.Context, deviceID string) ([]Service, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, device_id, name, protocol, local_addr, public_port, enabled, created_at
		FROM services WHERE device_id = ? ORDER BY created_at ASC`, deviceID)
	if err != nil {
		return nil, fmt.Errorf("列设备服务: %w", err)
	}
	defer rows.Close()
	return collectServices(rows)
}

func (s *Store) ListAllServices(ctx context.Context) ([]Service, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, device_id, name, protocol, local_addr, public_port, enabled, created_at
		FROM services ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("列全部服务: %w", err)
	}
	defer rows.Close()
	return collectServices(rows)
}

// ServiceListItem 是 services JOIN devices 的扁平视图，避免 N+1 query。
type ServiceListItem struct {
	Service
	DeviceName string
}

// ListServicesJoined 一次性返回 service + 所属 device 的 name。
// deviceID 为空表示返回所有设备的服务。
func (s *Store) ListServicesJoined(ctx context.Context, deviceID string) ([]ServiceListItem, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if deviceID == "" {
		rows, err = s.db.QueryContext(ctx, `
			SELECT s.id, s.device_id, s.name, s.protocol, s.local_addr, s.public_port, s.enabled, s.created_at,
			       d.name
			FROM services s JOIN devices d ON s.device_id = d.id
			ORDER BY s.created_at ASC`)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT s.id, s.device_id, s.name, s.protocol, s.local_addr, s.public_port, s.enabled, s.created_at,
			       d.name
			FROM services s JOIN devices d ON s.device_id = d.id
			WHERE s.device_id = ?
			ORDER BY s.created_at ASC`, deviceID)
	}
	if err != nil {
		return nil, fmt.Errorf("查询 services join: %w", err)
	}
	defer rows.Close()

	var out []ServiceListItem
	for rows.Next() {
		var (
			it      ServiceListItem
			enabled int
			created string
		)
		if err := rows.Scan(&it.ID, &it.DeviceID, &it.Name, &it.Protocol, &it.LocalAddr,
			&it.PublicPort, &enabled, &created, &it.DeviceName); err != nil {
			return nil, fmt.Errorf("扫描 service+device 行: %w", err)
		}
		it.Enabled = enabled != 0
		c, err := time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, fmt.Errorf("解析 created_at: %w", err)
		}
		it.CreatedAt = c
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) UsedPublicPorts(ctx context.Context) ([]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT public_port FROM services`)
	if err != nil {
		return nil, fmt.Errorf("查端口占用: %w", err)
	}
	defer rows.Close()
	var ports []int
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		ports = append(ports, p)
	}
	return ports, rows.Err()
}

// SetServiceEnabled 切换 service.enabled。
func (s *Store) SetServiceEnabled(ctx context.Context, id string, enabled bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE services SET enabled = ? WHERE id = ?`, boolToInt(enabled), id)
	if err != nil {
		return fmt.Errorf("更新 service enabled: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteService(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM services WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删 service: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func scanService(row *sql.Row) (Service, error) {
	var (
		sv      Service
		enabled int
		created string
	)
	if err := row.Scan(&sv.ID, &sv.DeviceID, &sv.Name, &sv.Protocol, &sv.LocalAddr,
		&sv.PublicPort, &enabled, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sv, ErrNotFound
		}
		return sv, fmt.Errorf("读 service: %w", err)
	}
	sv.Enabled = enabled != 0
	c, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return sv, fmt.Errorf("解析 created_at: %w", err)
	}
	sv.CreatedAt = c
	return sv, nil
}

func collectServices(rows *sql.Rows) ([]Service, error) {
	var out []Service
	for rows.Next() {
		var (
			sv      Service
			enabled int
			created string
		)
		if err := rows.Scan(&sv.ID, &sv.DeviceID, &sv.Name, &sv.Protocol, &sv.LocalAddr,
			&sv.PublicPort, &enabled, &created); err != nil {
			return nil, fmt.Errorf("扫描 service 行: %w", err)
		}
		sv.Enabled = enabled != 0
		c, err := time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, fmt.Errorf("解析 created_at: %w", err)
		}
		sv.CreatedAt = c
		out = append(out, sv)
	}
	return out, rows.Err()
}
