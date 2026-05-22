package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zhiying8710/oh-my-lan/internal/auth"
	"github.com/zhiying8710/oh-my-lan/internal/proto"
	"github.com/zhiying8710/oh-my-lan/internal/server/api"
	"github.com/zhiying8710/oh-my-lan/internal/store"
)

// newAdminBearer 直接写一条 admin token 进 DB，省去 CLI 流程。
// 与 server_test.go 中重复的小段——但 server_test.go 那一份没有抽到 helper，
// 现阶段不动既有测试结构。
func newAdminBearer(t *testing.T, srv *Server) string {
	t.Helper()
	raw := "test-admin-bark"
	if err := srv.store.CreateAdminToken(t.Context(), store.AdminToken{
		ID: "at-bark", TokenHash: auth.HashSecret(raw), Label: "bark", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	return "Bearer " + raw
}

func TestBarkGet_DefaultIsDisabled(t *testing.T) {
	ts, srv := newTestServer(t)
	bearer := newAdminBearer(t, srv)
	got := mustDoJSON[proto.BarkSettingsDTO](t, ts, http.MethodGet, "/api/admin/bark", bearer, "", http.StatusOK)
	// 从未配置过：返回 zero value，Enabled=false，URL 空
	if got.Enabled {
		t.Errorf("默认应 disabled，got %+v", got)
	}
	if got.BarkURL != "" {
		t.Errorf("默认 URL 应空，got %q", got.BarkURL)
	}
}

func TestBarkPut_RejectsBadURLWhenEnabled(t *testing.T) {
	ts, srv := newTestServer(t)
	bearer := newAdminBearer(t, srv)

	cases := []struct {
		name string
		url  string
	}{
		{"empty", ""},
		{"no-scheme", "api.day.app/key"},
		{"ftp scheme", "ftp://api.day.app/key"},
		{"no host", "https:///path"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doRaw(t, ts, http.MethodPut, "/api/admin/bark", bearer,
				toJSON(t, proto.BarkSettingsDTO{
					Enabled: true, BarkURL: c.url, OfflineThresholdSeconds: 180,
				}),
				http.StatusBadRequest)
		})
	}
}

func TestBarkPut_AcceptsValidConfig(t *testing.T) {
	ts, srv := newTestServer(t)
	bearer := newAdminBearer(t, srv)

	// 启用 + 合法 URL：成功，回读一致
	body := proto.BarkSettingsDTO{
		Enabled:                 true,
		BarkURL:                 "https://api.day.app/abc123",
		OfflineThresholdSeconds: 300,
	}
	mustDoJSON[proto.BarkSettingsDTO](t, ts, http.MethodPut, "/api/admin/bark", bearer,
		toJSON(t, body), http.StatusOK)
	got := mustDoJSON[proto.BarkSettingsDTO](t, ts, http.MethodGet, "/api/admin/bark", bearer, "", http.StatusOK)
	if got.BarkURL != body.BarkURL || !got.Enabled || got.OfflineThresholdSeconds != 300 {
		t.Errorf("回读不一致: %+v want %+v", got, body)
	}
}

func TestBarkPut_ClampsThreshold(t *testing.T) {
	ts, srv := newTestServer(t)
	bearer := newAdminBearer(t, srv)

	// 阈值 < 30 应被钳到 30（避免误报频繁）
	mustDoJSON[proto.BarkSettingsDTO](t, ts, http.MethodPut, "/api/admin/bark", bearer,
		toJSON(t, proto.BarkSettingsDTO{
			Enabled: false, BarkURL: "", OfflineThresholdSeconds: 5,
		}),
		http.StatusOK)
	got := mustDoJSON[proto.BarkSettingsDTO](t, ts, http.MethodGet, "/api/admin/bark", bearer, "", http.StatusOK)
	if got.OfflineThresholdSeconds != 30 {
		t.Errorf("阈值应钳到 30，got %d", got.OfflineThresholdSeconds)
	}
}

func TestBarkPut_DisabledSkipsURLValidation(t *testing.T) {
	ts, srv := newTestServer(t)
	bearer := newAdminBearer(t, srv)

	// 禁用状态保存空 URL 应通过——历史教训：禁用时强校验 URL 会导致用户没法保存
	// "我先关掉它，回头再填 URL" 这条直觉路径。
	mustDoJSON[proto.BarkSettingsDTO](t, ts, http.MethodPut, "/api/admin/bark", bearer,
		toJSON(t, proto.BarkSettingsDTO{
			Enabled: false, BarkURL: "", OfflineThresholdSeconds: 180,
		}),
		http.StatusOK)
}

func TestBarkTest_NoURL_400(t *testing.T) {
	ts, srv := newTestServer(t)
	bearer := newAdminBearer(t, srv)
	// 未配置 URL → 400，不会真发 push
	doRaw(t, ts, http.MethodPost, "/api/admin/bark/test", bearer, "", http.StatusBadRequest)
}

func TestBarkTest_PushSuccess_204(t *testing.T) {
	ts, srv := newTestServer(t)
	bearer := newAdminBearer(t, srv)

	// 假 bark 端点：成功响应 200
	var hits int32
	fakeBark := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		// bark URL 形如 <base>/<title>/<body>?...；我们的 server 用 PathEscape
		// 把中文标题转成 %XX，这里只验路径前缀
		if r.Method != http.MethodPost {
			t.Errorf("bark POST 才对，got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(fakeBark.Close)

	// 直接写 store 设置 URL，绕过 PUT 校验流程更直接
	if err := srv.store.SetBarkSettings(t.Context(), store.BarkSettings{
		Enabled: true, BarkURL: fakeBark.URL, OfflineThresholdSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}

	doRaw(t, ts, http.MethodPost, "/api/admin/bark/test", bearer, "", http.StatusNoContent)
	if atomic.LoadInt32(&hits) != 1 {
		t.Errorf("期望 fakeBark 被打中 1 次，got %d", hits)
	}

	// audit 应该记录 ok=true
	au := mustDoJSON[proto.AdminListAuditResponse](t, ts, http.MethodGet, "/api/admin/audit?limit=50", bearer, "", http.StatusOK)
	ok := false
	for _, e := range au.Entries {
		if e.Action == api.ActionBarkTest && strings.Contains(e.Detail, `"ok":true`) {
			ok = true
		}
	}
	if !ok {
		t.Errorf("audit 应记录 bark.test ok=true: %+v", au.Entries)
	}
}

func TestBarkTest_UpstreamFailure_502(t *testing.T) {
	ts, srv := newTestServer(t)
	bearer := newAdminBearer(t, srv)

	// 假 bark 端点回 500，模拟设备 key 失效之类——server 应该把它翻译为 502 Bad Gateway。
	fakeBark := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(fakeBark.Close)

	if err := srv.store.SetBarkSettings(t.Context(), store.BarkSettings{
		Enabled: true, BarkURL: fakeBark.URL, OfflineThresholdSeconds: 60,
	}); err != nil {
		t.Fatal(err)
	}

	doRaw(t, ts, http.MethodPost, "/api/admin/bark/test", bearer, "", http.StatusBadGateway)

	// audit 应该记录 ok=false（含 err 字段）
	au := mustDoJSON[proto.AdminListAuditResponse](t, ts, http.MethodGet, "/api/admin/audit?limit=50", bearer, "", http.StatusOK)
	failSeen := false
	for _, e := range au.Entries {
		if e.Action == api.ActionBarkTest && strings.Contains(e.Detail, `"ok":false`) {
			failSeen = true
		}
	}
	if !failSeen {
		t.Errorf("audit 应记录 bark.test ok=false: %+v", au.Entries)
	}
}

func TestBarkPut_RejectsBadJSON(t *testing.T) {
	ts, srv := newTestServer(t)
	bearer := newAdminBearer(t, srv)
	doRaw(t, ts, http.MethodPut, "/api/admin/bark", bearer, "not-json", http.StatusBadRequest)
}
