package server

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zhiying8710/oh-my-lan/internal/store"
)

// maybePushBarkAlerts 是 offline reaper 调的，没 admin handler 走它，只能直测。
// 这套测试覆盖几条关键不变量：
//   1) bark.Enabled=false → 早返，不查 devices，更不发 HTTP
//   2) 设备 online（last_seen 在阈值内）→ 不推
//   3) 设备 offline + 没推过 → 推一次 + 写 alert state
//   4) 同一离线状态再 tick 一次 → 不重复推
//   5) 设备回 online → 清 alert state，下次掉线能再推

// pushSpy 替身：充当 bark 端点；记录调用次数；可被切换为 fail mode。
type pushSpy struct {
	hits int32
	fail bool
	srv  *httptest.Server
}

func newPushSpy(t *testing.T) *pushSpy {
	t.Helper()
	p := &pushSpy{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&p.hits, 1)
		if p.fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *pushSpy) Hits() int32 { return atomic.LoadInt32(&p.hits) }
func (p *pushSpy) URL() string { return p.srv.URL }

// 简化的 reaper 输入：手动写一个 device + last_seen + alert state，直接调 maybePushBarkAlerts。
// 不通过 HTTP——bark push 不是 HTTP handler。

func setBark(t *testing.T, srv *Server, url string, threshold int, enabled bool) {
	t.Helper()
	if err := srv.store.SetBarkSettings(t.Context(), store.BarkSettings{
		Enabled: enabled, BarkURL: url, OfflineThresholdSeconds: threshold,
	}); err != nil {
		t.Fatal(err)
	}
}

func makeDeviceWithLastSeen(t *testing.T, srv *Server, id, name string, lastSeen *time.Time) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC()
	if err := srv.store.CreateDevice(ctx, store.Device{
		ID: id, Name: name, TunnelSecret: "x", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if lastSeen != nil {
		// UpdateDeviceStatus 把 status + last_seen_at 一起写
		if err := srv.store.UpdateDeviceStatus(ctx, id, store.DeviceStatusOnline, *lastSeen); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBarkPush_DisabledNoOp(t *testing.T) {
	_, srv := newTestServer(t)
	spy := newPushSpy(t)
	// 配 URL 但 disabled
	setBark(t, srv, spy.URL(), 60, false)
	ago := time.Now().Add(-time.Hour)
	makeDeviceWithLastSeen(t, srv, "d1", "alpha", &ago)

	srv.maybePushBarkAlerts(t.Context())
	if spy.Hits() != 0 {
		t.Errorf("disabled bark 不应发请求, got %d", spy.Hits())
	}
}

func TestBarkPush_OnlineDeviceSkipped(t *testing.T) {
	_, srv := newTestServer(t)
	spy := newPushSpy(t)
	setBark(t, srv, spy.URL(), 60, true)
	// last_seen 5 秒前——在 60s 阈值内 → 视为 online
	recent := time.Now().Add(-5 * time.Second)
	makeDeviceWithLastSeen(t, srv, "d1", "alpha", &recent)

	srv.maybePushBarkAlerts(t.Context())
	if spy.Hits() != 0 {
		t.Errorf("online device 不应触发推送, got %d", spy.Hits())
	}
}

func TestBarkPush_OfflineDeviceTriggers(t *testing.T) {
	_, srv := newTestServer(t)
	spy := newPushSpy(t)
	setBark(t, srv, spy.URL(), 60, true)
	// last_seen 1 小时前 → 超 60s 阈值
	stale := time.Now().Add(-time.Hour)
	makeDeviceWithLastSeen(t, srv, "d1", "alpha", &stale)

	srv.maybePushBarkAlerts(t.Context())
	if spy.Hits() != 1 {
		t.Fatalf("offline device 应推一次, got %d", spy.Hits())
	}

	// 第二次 tick：alert_state 已写 → 不重复推
	srv.maybePushBarkAlerts(t.Context())
	if spy.Hits() != 1 {
		t.Errorf("已 alerted 不应再推, got %d", spy.Hits())
	}

	// 验证 alert_state 已落
	alerted, _ := srv.store.IsDeviceAlerted(t.Context(), "d1")
	if !alerted {
		t.Errorf("应该写过 alert_state")
	}
}

func TestBarkPush_BackOnlineClearsState(t *testing.T) {
	_, srv := newTestServer(t)
	spy := newPushSpy(t)
	setBark(t, srv, spy.URL(), 60, true)
	stale := time.Now().Add(-time.Hour)
	makeDeviceWithLastSeen(t, srv, "d1", "alpha", &stale)

	// 触发首次推送
	srv.maybePushBarkAlerts(t.Context())
	if spy.Hits() != 1 {
		t.Fatal("初次离线应推")
	}

	// 把 last_seen 拨到现在 → device 回 online → 清 alert state
	recent := time.Now()
	if err := srv.store.UpdateDeviceStatus(t.Context(), "d1", store.DeviceStatusOnline, recent); err != nil {
		t.Fatal(err)
	}
	srv.maybePushBarkAlerts(t.Context())
	alerted, _ := srv.store.IsDeviceAlerted(t.Context(), "d1")
	if alerted {
		t.Errorf("回 online 应清 alert state")
	}

	// 再让它掉线 → 应该再推一次（state 已清）
	stale2 := time.Now().Add(-2 * time.Hour)
	if err := srv.store.UpdateDeviceStatus(t.Context(), "d1", store.DeviceStatusOffline, stale2); err != nil {
		t.Fatal(err)
	}
	srv.maybePushBarkAlerts(t.Context())
	if spy.Hits() != 2 {
		t.Errorf("二次离线应再推一次, total hits=%d", spy.Hits())
	}
}

func TestBarkPush_NeverOnlineDeviceTriggers(t *testing.T) {
	_, srv := newTestServer(t)
	spy := newPushSpy(t)
	setBark(t, srv, spy.URL(), 60, true)
	// 设备注册后从未心跳 → last_seen_at = NULL → maybePushBarkAlerts 视为 offline
	makeDeviceWithLastSeen(t, srv, "d1", "alpha", nil)

	srv.maybePushBarkAlerts(t.Context())
	if spy.Hits() != 1 {
		t.Errorf("从未心跳的设备应被视为 offline 并推送, got %d", spy.Hits())
	}
}

func TestBarkPush_UpstreamFailureDoesNotMarkAlerted(t *testing.T) {
	_, srv := newTestServer(t)
	spy := newPushSpy(t)
	spy.fail = true // bark 端点回 500
	setBark(t, srv, spy.URL(), 60, true)
	stale := time.Now().Add(-time.Hour)
	makeDeviceWithLastSeen(t, srv, "d1", "alpha", &stale)

	srv.maybePushBarkAlerts(t.Context())
	if spy.Hits() != 1 {
		t.Fatal("应尝试推一次")
	}

	// 推失败时不能写 alert state——下次 tick 必须重试。
	// 否则用户重启 bark 服务器后永远等不到补推。
	alerted, _ := srv.store.IsDeviceAlerted(t.Context(), "d1")
	if alerted {
		t.Errorf("推失败不应 mark alerted（应可重试）")
	}

	// 第二次 tick 验证：仍尝试推（hits 增加）
	srv.maybePushBarkAlerts(t.Context())
	if spy.Hits() != 2 {
		t.Errorf("失败后应在下次 tick 重试, got %d", spy.Hits())
	}
}
