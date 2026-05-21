package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestMigrateRunsAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	// 再跑一次 migrate 不应该报错
	if err := migrate(ctx, s.DB()); err != nil {
		t.Fatalf("二次 migrate 失败: %v", err)
	}

	var n int
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Errorf("schema_migrations 应有记录, 实际 %d", n)
	}
}

func TestDeviceCRUD(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	dev := Device{
		ID:               "dev-1",
		Name:             "macbook",
		TunnelSecret: "hash-1",
		CreatedAt:        time.Now().UTC(),
	}
	if err := s.CreateDevice(ctx, dev); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.GetDeviceByID(ctx, "dev-1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "macbook" || got.Status != DeviceStatusOffline {
		t.Errorf("意外的设备: %+v", got)
	}

	now := time.Now().UTC()
	if err := s.UpdateDeviceStatus(ctx, "dev-1", DeviceStatusOnline, now); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, _ = s.GetDeviceByID(ctx, "dev-1")
	if got.Status != DeviceStatusOnline || got.LastSeenAt == nil {
		t.Errorf("状态未生效: %+v", got)
	}

	list, err := s.ListDevices(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List: err=%v len=%d", err, len(list))
	}

	if _, err := s.GetDeviceByID(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("缺失 device 应返回 ErrNotFound, got %v", err)
	}
}

func TestEnrollmentTokenConsume(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	tok := EnrollmentToken{
		ID:        "tok-1",
		TokenHash: "h1",
		ExpiresAt: time.Now().Add(time.Hour),
		CreatedAt: time.Now(),
	}
	if err := s.CreateEnrollmentToken(ctx, tok); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	dev := Device{ID: "dev-2", Name: "nas", TunnelSecret: "sh", CreatedAt: time.Now()}
	if err := s.ConsumeEnrollmentToken(ctx, "h1", dev); err != nil {
		t.Fatalf("Consume: %v", err)
	}

	// 二次消费应失败
	if err := s.ConsumeEnrollmentToken(ctx, "h1", Device{ID: "dev-3", Name: "x", TunnelSecret: "y", CreatedAt: time.Now()}); !errors.Is(err, ErrNotFound) {
		t.Errorf("二次消费应 ErrNotFound, got %v", err)
	}

	got, err := s.GetEnrollmentTokenByHash(ctx, "h1")
	if err != nil {
		t.Fatalf("GetToken: %v", err)
	}
	if got.UsedAt == nil || got.UsedByDeviceID == nil || *got.UsedByDeviceID != "dev-2" {
		t.Errorf("token 标记不正确: %+v", got)
	}
}

func TestServiceCRUDAndPortUnique(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	dev := Device{ID: "dev-1", Name: "n1", TunnelSecret: "h", CreatedAt: time.Now()}
	if err := s.CreateDevice(ctx, dev); err != nil {
		t.Fatal(err)
	}

	svc := Service{
		ID: "svc-1", DeviceID: "dev-1", Name: "ssh",
		Protocol: ProtocolTCP, LocalAddr: "127.0.0.1:22",
		PublicPort: 40001, Enabled: true, CreatedAt: time.Now(),
	}
	if err := s.CreateService(ctx, svc); err != nil {
		t.Fatalf("CreateService: %v", err)
	}

	dup := svc
	dup.ID = "svc-2"
	dup.Name = "ssh2"
	if err := s.CreateService(ctx, dup); err == nil {
		t.Error("相同 public_port 应触发唯一约束")
	}

	ports, err := s.UsedPublicPorts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ports) != 1 || ports[0] != 40001 {
		t.Errorf("UsedPublicPorts 异常: %v", ports)
	}

	list, err := s.ListServicesByDevice(ctx, "dev-1")
	if err != nil || len(list) != 1 {
		t.Fatalf("ListByDevice: err=%v len=%d", err, len(list))
	}

	if err := s.DeleteService(ctx, "svc-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetService(ctx, "svc-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("删除后应 ErrNotFound, got %v", err)
	}
}

func TestMarkStaleDevicesOffline(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	now := time.Now().UTC()
	mustCreateDev := func(id, name string, status string, lastSeen *time.Time) {
		t.Helper()
		if err := s.CreateDevice(ctx, Device{
			ID: id, Name: name, TunnelSecret: "x",
			Status: status, CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		if lastSeen != nil {
			if err := s.UpdateDeviceStatus(ctx, id, status, *lastSeen); err != nil {
				t.Fatal(err)
			}
		}
	}
	fresh := now.Add(-10 * time.Second)
	stale := now.Add(-5 * time.Minute)

	mustCreateDev("fresh-online", "f", DeviceStatusOnline, &fresh)
	mustCreateDev("stale-online", "s", DeviceStatusOnline, &stale)
	mustCreateDev("never-seen-online", "n", DeviceStatusOnline, nil)
	mustCreateDev("offline-stale", "o", DeviceStatusOffline, &stale)

	n, err := s.MarkStaleDevicesOffline(ctx, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	// stale-online 和 never-seen-online 应被标 offline；fresh 不动；offline-stale 已是 offline 不动
	if n != 2 {
		t.Errorf("应标记 2 个, got %d", n)
	}
	got, _ := s.GetDeviceByID(ctx, "stale-online")
	if got.Status != DeviceStatusOffline {
		t.Errorf("stale 设备未标 offline: %s", got.Status)
	}
	got, _ = s.GetDeviceByID(ctx, "fresh-online")
	if got.Status != DeviceStatusOnline {
		t.Errorf("fresh 设备不应改: %s", got.Status)
	}
}

func TestDeleteDevice_CascadeServices(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if err := s.CreateDevice(ctx, Device{ID: "d", Name: "n", TunnelSecret: "x", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateService(ctx, Service{
		ID: "svc", DeviceID: "d", Name: "ssh", Protocol: "tcp",
		LocalAddr: "127.0.0.1:22", PublicPort: 41001, Enabled: true, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDevice(ctx, "d"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetService(ctx, "svc"); !errors.Is(err, ErrNotFound) {
		t.Errorf("级联应删 service, got %v", err)
	}
	if err := s.DeleteDevice(ctx, "d"); !errors.Is(err, ErrNotFound) {
		t.Errorf("二次删除应 ErrNotFound, got %v", err)
	}
}

func TestSetServiceEnabled(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if err := s.CreateDevice(ctx, Device{ID: "d", Name: "n", TunnelSecret: "x", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateService(ctx, Service{
		ID: "svc", DeviceID: "d", Name: "ssh", Protocol: "tcp",
		LocalAddr: "127.0.0.1:22", PublicPort: 41001, Enabled: true, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetServiceEnabled(ctx, "svc", false); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetService(ctx, "svc")
	if got.Enabled {
		t.Error("应已 disabled")
	}
	if err := s.SetServiceEnabled(ctx, "missing", false); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的 service 应 ErrNotFound, got %v", err)
	}
}

func TestForwardCRUDAndCascade(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := time.Now()

	// 准备：B 设备 + B 的 service；A 设备
	if err := s.CreateDevice(ctx, Device{ID: "B", Name: "b", TunnelSecret: "x", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateDevice(ctx, Device{ID: "A", Name: "a", TunnelSecret: "y", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateService(ctx, Service{
		ID: "svc-B-ssh", DeviceID: "B", Name: "ssh", Protocol: "tcp",
		LocalAddr: "127.0.0.1:22", PublicPort: 41010, Enabled: true, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	// A forward B 的 ssh 到本地 8022
	if err := s.CreateForward(ctx, Forward{
		ID: "fwd-1", OwnerDeviceID: "A", RemoteServiceID: "svc-B-ssh",
		LocalPort: 8022, Enabled: true, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetForward(ctx, "fwd-1")
	if err != nil || got.LocalPort != 8022 {
		t.Errorf("Get: %v %+v", err, got)
	}

	list, err := s.ListForwardsByOwner(ctx, "A")
	if err != nil || len(list) != 1 {
		t.Fatalf("List: err=%v len=%d", err, len(list))
	}

	// 同一 owner + local_port 唯一
	if err := s.CreateForward(ctx, Forward{
		ID: "fwd-dup", OwnerDeviceID: "A", RemoteServiceID: "svc-B-ssh",
		LocalPort: 8022, Enabled: true, CreatedAt: now,
	}); err == nil {
		t.Error("应触发唯一约束")
	}

	// 删除 service 应级联删除 forward
	if err := s.DeleteService(ctx, "svc-B-ssh"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetForward(ctx, "fwd-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("级联删除应使 forward 消失, got %v", err)
	}
}

func TestAdminTokenCRUD(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := time.Now()

	tok := AdminToken{ID: "t1", TokenHash: "h1", Label: "laptop", CreatedAt: now}
	if err := s.CreateAdminToken(ctx, tok); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAdminTokenByHash(ctx, "h1")
	if err != nil || got.Label != "laptop" {
		t.Errorf("Get: %v %+v", err, got)
	}

	// UNIQUE token_hash
	if err := s.CreateAdminToken(ctx, AdminToken{ID: "t2", TokenHash: "h1", Label: "dup", CreatedAt: now}); err == nil {
		t.Error("重复 hash 应失败")
	}

	if err := s.TouchAdminToken(ctx, "t1", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetAdminTokenByHash(ctx, "h1")
	if got.LastUsedAt == nil {
		t.Error("touch 后 LastUsedAt 应非 nil")
	}

	list, _ := s.ListAdminTokens(ctx)
	if len(list) != 1 {
		t.Errorf("list: %d", len(list))
	}

	if err := s.DeleteAdminToken(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAdminTokenByHash(ctx, "h1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("删除后 Get 应 ErrNotFound, got %v", err)
	}
	if err := s.DeleteAdminToken(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的删除应 ErrNotFound, got %v", err)
	}
}

func TestSetForwardEnabled(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := time.Now()

	if err := s.CreateDevice(ctx, Device{ID: "A", Name: "a", TunnelSecret: "x", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateDevice(ctx, Device{ID: "B", Name: "b", TunnelSecret: "y", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateService(ctx, Service{
		ID: "svc", DeviceID: "B", Name: "x", Protocol: "tcp",
		LocalAddr: "127.0.0.1:1", PublicPort: 41030, Enabled: true, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateForward(ctx, Forward{
		ID: "f", OwnerDeviceID: "A", RemoteServiceID: "svc",
		LocalPort: 9001, Enabled: true, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.SetForwardEnabled(ctx, "f", false); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetForward(ctx, "f")
	if got.Enabled {
		t.Error("应已 disabled")
	}
	if err := s.SetForwardEnabled(ctx, "missing", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的 forward 应 ErrNotFound, got %v", err)
	}
}

func TestForwardCascadeOnOwnerDelete(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := time.Now()

	if err := s.CreateDevice(ctx, Device{ID: "A", Name: "a", TunnelSecret: "x", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateDevice(ctx, Device{ID: "B", Name: "b", TunnelSecret: "y", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateService(ctx, Service{
		ID: "svc", DeviceID: "B", Name: "x", Protocol: "tcp",
		LocalAddr: "127.0.0.1:1", PublicPort: 41020, Enabled: true, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateForward(ctx, Forward{
		ID: "f", OwnerDeviceID: "A", RemoteServiceID: "svc",
		LocalPort: 9000, Enabled: true, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDevice(ctx, "A"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetForward(ctx, "f"); !errors.Is(err, ErrNotFound) {
		t.Errorf("owner 删除应级联删 forward, got %v", err)
	}
}

func TestServiceCascadeDeleteOnDevice(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if err := s.CreateDevice(ctx, Device{ID: "d", Name: "n", TunnelSecret: "h", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateService(ctx, Service{
		ID: "s", DeviceID: "d", Name: "ssh", Protocol: "tcp",
		LocalAddr: "127.0.0.1:22", PublicPort: 40010, Enabled: true, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.DB().ExecContext(ctx, `DELETE FROM devices WHERE id='d'`); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListAllServices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("级联删除应清空 services, 仍有 %d 条", len(list))
	}
}
