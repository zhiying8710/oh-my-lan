package server

import (
	"context"
	"testing"
	"time"

	"github.com/zhiying8710/oh-my-lan/internal/auth"
	"github.com/zhiying8710/oh-my-lan/internal/store"
)

// TestReloadChiselUsers_SyncsAllDevices: 重启场景。store 里有 N 个设备，
// reloadChiselUsers 调完后 tunnel.UserIndex 应有同样数量的 user。
//
// 测试 chisel tunnel 实例没有暴露 ListUsers，但 AddDevice 重复调同 ID 不会出错
// （chisel 内部 map 覆盖），换言之我们只能反向验证：没 panic + 返回 nil。
// 更进一步要做行为断言需要给 tunnel 加个 Has(id) 方法，超出本次 scope。
func TestReloadChiselUsers_SyncsAllDevices(t *testing.T) {
	_, srv := newTestServer(t)
	ctx := t.Context()

	now := time.Now().UTC()
	for _, id := range []string{"d1", "d2", "d3"} {
		if err := srv.store.CreateDevice(ctx, store.Device{
			ID: id, Name: id, TunnelSecret: "secret-" + id, CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := srv.reloadChiselUsers(ctx); err != nil {
		t.Fatalf("reloadChiselUsers: %v", err)
	}
}

func TestReloadChiselUsers_EmptyStore_OK(t *testing.T) {
	_, srv := newTestServer(t)
	if err := srv.reloadChiselUsers(t.Context()); err != nil {
		t.Fatalf("空 store 也应 OK: %v", err)
	}
}

// TestRunOfflineReaper_CancellationStops: ctx cancel 后 reaper 应迅速退出，
// 不依赖 ticker——否则 server shutdown 会等到下一个 tick（45s）才返回。
func TestRunOfflineReaper_CancellationStops(t *testing.T) {
	_, srv := newTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { srv.runOfflineReaper(ctx); close(done) }()

	// 让它跑一会儿确保进了 for-select
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("ctx cancel 后 reaper 没退出")
	}
}

// TestRunSessionReaper_FirstPassCleansExpired: 启动时先跑一遍——
// 已过期的 session 在 reaper 第一次执行后就应被清除，无需等 1 小时 tick。
func TestRunSessionReaper_FirstPassCleansExpired(t *testing.T) {
	_, srv := newTestServer(t)
	ctx := t.Context()

	// 造一个 user + 一个已过期的 session
	hash, _ := auth.HashPassword("x")
	if err := srv.store.CreateAdminUser(ctx, store.AdminUser{
		ID: "u", Username: "u", PasswordHash: hash,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	expired := "sess_expired"
	if err := srv.store.CreateSession(ctx, store.Session{
		ID: "s-expired", UserID: "u", TokenHash: auth.HashSecret(expired),
		CreatedAt: time.Now().Add(-time.Hour),
		ExpiresAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	// 起 reaper，让它跑首轮立即 cancel
	rctx, cancel := context.WithCancel(context.Background())
	go srv.runSessionReaper(rctx)
	time.Sleep(50 * time.Millisecond) // 首轮 cleanup
	cancel()

	// 验证 session 不再可查（GetActiveSessionByHash 排除 expires_at<=now，
	// 所以这里用一个未来时间 + 直接 SQL 验证更直接，但 store 没暴露 raw select。
	// 退而求其次：再次拿 active session 用过去时间应仍找不到——已被 reaper DELETE。）
	if _, err := srv.store.GetActiveSessionByHash(ctx, auth.HashSecret(expired), time.Now().Add(-2*time.Hour)); err == nil {
		t.Errorf("过期 session 应被 reaper DELETE 后从 DB 消失")
	}
}
