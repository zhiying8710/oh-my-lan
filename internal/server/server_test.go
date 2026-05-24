package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zhiying8710/oh-my-lan/internal/auth"
	"github.com/zhiying8710/oh-my-lan/internal/enroll"
	"github.com/zhiying8710/oh-my-lan/internal/proto"
	"github.com/zhiying8710/oh-my-lan/internal/server/api"
	"github.com/zhiying8710/oh-my-lan/internal/store"
	"github.com/zhiying8710/oh-my-lan/internal/tunnel"
)

// testSSHPubkey 是 enroll 测试用的合法形态 ed25519 公钥。
// enroll handler 现在强制要求 ssh_pubkey；测试只验证 schema/handler 行为，
// 不真去 useradd —— newTestServer 不挂 SSHAcct（nil），handler 跳过 Provision。
const testSSHPubkey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBeAbCdEfGhIjKlMnOpQrStUvWxYzAbCdEfGhIjKlMno test@oml"

// newTestServer 拼装一个未真正监听的 Server，仅注入 mux 给 httptest 用。
// chisel tunnel 仅用其 AddDevice / Fingerprint 等内存方法，不开监听。
// sshacct 留 nil → handler 跳过真 useradd，保持单元测试不依赖系统账号。
func newTestServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	st, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	tun, err := tunnel.NewServer(tunnel.ServerConfig{ListenAddr: ":0"})
	if err != nil {
		t.Fatalf("tunnel.NewServer: %v", err)
	}
	t.Cleanup(func() { _ = tun.Close() })

	s := &Server{
		logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		store:               st,
		enroll:              enroll.New(st),
		tunnel:              tun,
		ports:               api.NewPortAllocator(st, 40000, 40010),
		chiselAdvertiseAddr: "test-vps:8443",
	}
	mux := http.NewServeMux()
	s.registerRoutes(mux)

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, s
}

func TestFullEnrollAndServiceFlow(t *testing.T) {
	ts, srv := newTestServer(t)

	// 1. 本地调用 issue token —— httptest.Server 的 RemoteAddr 是 127.0.0.1
	tokResp := mustDoJSON[proto.IssueTokenResponse](t, ts, http.MethodPost, "/api/enroll/tokens",
		"", `{"ttl_seconds":3600}`, http.StatusCreated)
	if tokResp.Token == "" {
		t.Fatal("token 为空")
	}

	// 2. enroll
	enrollResp := mustDoJSON[proto.EnrollDeviceResponse](t, ts, http.MethodPost, "/api/devices/enroll",
		"",
		toJSON(t, proto.EnrollDeviceRequest{Token: tokResp.Token, DeviceName: "test-dev", SSHPubkey: testSSHPubkey}),
		http.StatusCreated)
	if enrollResp.DeviceID == "" || enrollResp.TunnelSecret == "" {
		t.Fatalf("enroll 返回不完整: %+v", enrollResp)
	}
	if enrollResp.ChiselAddr != srv.chiselAdvertiseAddr {
		t.Errorf("ChiselAddr=%q want %q", enrollResp.ChiselAddr, srv.chiselAdvertiseAddr)
	}

	bearer := "Bearer " + enrollResp.DeviceID + "." + enrollResp.TunnelSecret

	// 3. add service
	svcResp := mustDoJSON[proto.ServiceDTO](t, ts, http.MethodPost, "/api/services",
		bearer,
		toJSON(t, proto.AddServiceRequest{Name: "ssh", Protocol: "tcp", LocalAddr: "127.0.0.1:22"}),
		http.StatusCreated)
	if svcResp.PublicPort < 40000 || svcResp.PublicPort > 40010 {
		t.Errorf("公网端口越界: %d", svcResp.PublicPort)
	}

	// 4. list services
	listResp := mustDoJSON[proto.ListServicesResponse](t, ts, http.MethodGet, "/api/services",
		bearer, "", http.StatusOK)
	if len(listResp.Services) != 1 || listResp.Services[0].ID != svcResp.ID {
		t.Errorf("list 返回不一致: %+v", listResp)
	}

	// 5. bootstrap
	bootResp := mustDoJSON[proto.BootstrapResponse](t, ts, http.MethodGet, "/api/devices/me/bootstrap",
		bearer, "", http.StatusOK)
	if len(bootResp.Remotes) != 1 || bootResp.Remotes[0].PublicPort != svcResp.PublicPort {
		t.Errorf("bootstrap 不一致: %+v", bootResp)
	}

	// 6. delete service
	doRaw(t, ts, http.MethodDelete, "/api/services/"+svcResp.ID, bearer, "", http.StatusNoContent)

	// 7. list 应为空
	listResp = mustDoJSON[proto.ListServicesResponse](t, ts, http.MethodGet, "/api/services",
		bearer, "", http.StatusOK)
	if len(listResp.Services) != 0 {
		t.Errorf("删除后 list 应为空, got %+v", listResp.Services)
	}
}

func TestAuthRejectsBadBearer(t *testing.T) {
	ts, _ := newTestServer(t)
	doRaw(t, ts, http.MethodGet, "/api/services", "", "", http.StatusUnauthorized)
	doRaw(t, ts, http.MethodGet, "/api/services", "Bearer no-dot", "", http.StatusUnauthorized)
	doRaw(t, ts, http.MethodGet, "/api/services", "Bearer x.y", "", http.StatusUnauthorized)
}

func TestEnrollRejectsBadToken(t *testing.T) {
	ts, _ := newTestServer(t)
	doRaw(t, ts, http.MethodPost, "/api/devices/enroll", "",
		toJSON(t, proto.EnrollDeviceRequest{Token: "wrong", DeviceName: "x", SSHPubkey: testSSHPubkey}),
		http.StatusUnauthorized)
}

func TestServiceEnableDisable(t *testing.T) {
	ts, _ := newTestServer(t)

	tokR := mustDoJSON[proto.IssueTokenResponse](t, ts, http.MethodPost, "/api/enroll/tokens", "", "", http.StatusCreated)
	dev := mustDoJSON[proto.EnrollDeviceResponse](t, ts, http.MethodPost, "/api/devices/enroll", "",
		toJSON(t, proto.EnrollDeviceRequest{Token: tokR.Token, DeviceName: "x", SSHPubkey: testSSHPubkey}), http.StatusCreated)
	bearer := "Bearer " + dev.DeviceID + "." + dev.TunnelSecret

	svc := mustDoJSON[proto.ServiceDTO](t, ts, http.MethodPost, "/api/services", bearer,
		toJSON(t, proto.AddServiceRequest{Name: "ssh", Protocol: "tcp", LocalAddr: "127.0.0.1:22"}),
		http.StatusCreated)
	if !svc.Enabled {
		t.Errorf("新创建应 enabled")
	}

	d := mustDoJSON[proto.ServiceDTO](t, ts, http.MethodPost, "/api/services/"+svc.ID+"/disable", bearer, "", http.StatusOK)
	if d.Enabled {
		t.Errorf("disable 后应 false")
	}

	// bootstrap 不应返回 disabled 服务
	boot := mustDoJSON[proto.BootstrapResponse](t, ts, http.MethodGet, "/api/devices/me/bootstrap", bearer, "", http.StatusOK)
	if len(boot.Remotes) != 0 {
		t.Errorf("disabled 服务不应出现在 bootstrap, got %d remotes", len(boot.Remotes))
	}

	e := mustDoJSON[proto.ServiceDTO](t, ts, http.MethodPost, "/api/services/"+svc.ID+"/enable", bearer, "", http.StatusOK)
	if !e.Enabled {
		t.Errorf("enable 后应 true")
	}

	boot = mustDoJSON[proto.BootstrapResponse](t, ts, http.MethodGet, "/api/devices/me/bootstrap", bearer, "", http.StatusOK)
	if len(boot.Remotes) != 1 {
		t.Errorf("enabled 后应回到 bootstrap")
	}
}

func TestForwardEnableDisable(t *testing.T) {
	ts, _ := newTestServer(t)

	tA := mustDoJSON[proto.IssueTokenResponse](t, ts, http.MethodPost, "/api/enroll/tokens", "", "", http.StatusCreated)
	tB := mustDoJSON[proto.IssueTokenResponse](t, ts, http.MethodPost, "/api/enroll/tokens", "", "", http.StatusCreated)
	a := mustDoJSON[proto.EnrollDeviceResponse](t, ts, http.MethodPost, "/api/devices/enroll", "",
		toJSON(t, proto.EnrollDeviceRequest{Token: tA.Token, DeviceName: "A", SSHPubkey: testSSHPubkey}), http.StatusCreated)
	b := mustDoJSON[proto.EnrollDeviceResponse](t, ts, http.MethodPost, "/api/devices/enroll", "",
		toJSON(t, proto.EnrollDeviceRequest{Token: tB.Token, DeviceName: "B", SSHPubkey: testSSHPubkey}), http.StatusCreated)
	bearerA := "Bearer " + a.DeviceID + "." + a.TunnelSecret
	bearerB := "Bearer " + b.DeviceID + "." + b.TunnelSecret

	svc := mustDoJSON[proto.ServiceDTO](t, ts, http.MethodPost, "/api/services", bearerB,
		toJSON(t, proto.AddServiceRequest{Name: "ssh", Protocol: "tcp", LocalAddr: "127.0.0.1:22"}),
		http.StatusCreated)
	fwd := mustDoJSON[proto.ForwardDTO](t, ts, http.MethodPost, "/api/forwards", bearerA,
		toJSON(t, proto.AddForwardRequest{RemoteServiceID: svc.ID, LocalPort: 8022}), http.StatusCreated)

	// disable → bootstrap.Locals 应为空
	d := mustDoJSON[proto.ForwardDTO](t, ts, http.MethodPost, "/api/forwards/"+fwd.ID+"/disable", bearerA, "", http.StatusOK)
	if d.Enabled {
		t.Error("disable 应 enabled=false")
	}
	boot := mustDoJSON[proto.BootstrapResponse](t, ts, http.MethodGet, "/api/devices/me/bootstrap", bearerA, "", http.StatusOK)
	if len(boot.Locals) != 0 {
		t.Errorf("disabled forward 不应出现在 bootstrap, got %d", len(boot.Locals))
	}

	// 跨设备 disable 应 404
	doRaw(t, ts, http.MethodPost, "/api/forwards/"+fwd.ID+"/disable", bearerB, "", http.StatusNotFound)

	// 重新 enable
	e := mustDoJSON[proto.ForwardDTO](t, ts, http.MethodPost, "/api/forwards/"+fwd.ID+"/enable", bearerA, "", http.StatusOK)
	if !e.Enabled {
		t.Error("enable 应 enabled=true")
	}
}

func TestForwardCRUD_HappyPath(t *testing.T) {
	ts, _ := newTestServer(t)

	// 注册 A、B；B 发布服务
	tA := mustDoJSON[proto.IssueTokenResponse](t, ts, http.MethodPost, "/api/enroll/tokens", "", "", http.StatusCreated)
	tB := mustDoJSON[proto.IssueTokenResponse](t, ts, http.MethodPost, "/api/enroll/tokens", "", "", http.StatusCreated)
	a := mustDoJSON[proto.EnrollDeviceResponse](t, ts, http.MethodPost, "/api/devices/enroll", "",
		toJSON(t, proto.EnrollDeviceRequest{Token: tA.Token, DeviceName: "A", SSHPubkey: testSSHPubkey}), http.StatusCreated)
	b := mustDoJSON[proto.EnrollDeviceResponse](t, ts, http.MethodPost, "/api/devices/enroll", "",
		toJSON(t, proto.EnrollDeviceRequest{Token: tB.Token, DeviceName: "B", SSHPubkey: testSSHPubkey}), http.StatusCreated)
	bearerA := "Bearer " + a.DeviceID + "." + a.TunnelSecret
	bearerB := "Bearer " + b.DeviceID + "." + b.TunnelSecret

	svc := mustDoJSON[proto.ServiceDTO](t, ts, http.MethodPost, "/api/services", bearerB,
		toJSON(t, proto.AddServiceRequest{Name: "ssh", Protocol: "tcp", LocalAddr: "127.0.0.1:22"}),
		http.StatusCreated)

	// A 看 services/all 能看到 B 的服务
	all := mustDoJSON[proto.ListAllServicesResponse](t, ts, http.MethodGet, "/api/services/all", bearerA, "", http.StatusOK)
	if len(all.Services) != 1 || all.Services[0].DeviceName != "B" {
		t.Fatalf("services/all 应看到 B 的服务: %+v", all)
	}

	// A 创建 forward 到 B 的服务
	fwd := mustDoJSON[proto.ForwardDTO](t, ts, http.MethodPost, "/api/forwards", bearerA,
		toJSON(t, proto.AddForwardRequest{RemoteServiceID: svc.ID, LocalPort: 8022}),
		http.StatusCreated)
	if fwd.RemotePublicPort != svc.PublicPort {
		t.Errorf("forward.RemotePublicPort=%d want %d", fwd.RemotePublicPort, svc.PublicPort)
	}
	if fwd.RemoteDeviceID != b.DeviceID {
		t.Errorf("forward.RemoteDeviceID=%s want %s", fwd.RemoteDeviceID, b.DeviceID)
	}

	// A 看自己的 forwards
	list := mustDoJSON[proto.ListForwardsResponse](t, ts, http.MethodGet, "/api/forwards", bearerA, "", http.StatusOK)
	if len(list.Forwards) != 1 {
		t.Fatalf("list forwards: %+v", list)
	}

	// bootstrap 包含 Locals
	boot := mustDoJSON[proto.BootstrapResponse](t, ts, http.MethodGet, "/api/devices/me/bootstrap", bearerA, "", http.StatusOK)
	if len(boot.Locals) != 1 || boot.Locals[0].LocalPort != 8022 || boot.Locals[0].RemotePublicPort != svc.PublicPort {
		t.Errorf("bootstrap.Locals 不对: %+v", boot.Locals)
	}

	// 重复 local_port 应 409
	doRaw(t, ts, http.MethodPost, "/api/forwards", bearerA,
		toJSON(t, proto.AddForwardRequest{RemoteServiceID: svc.ID, LocalPort: 8022}),
		http.StatusConflict)

	// B 看不到 A 的 forwards
	listB := mustDoJSON[proto.ListForwardsResponse](t, ts, http.MethodGet, "/api/forwards", bearerB, "", http.StatusOK)
	if len(listB.Forwards) != 0 {
		t.Errorf("B 不应看见 A 的 forwards")
	}

	// B 删不掉 A 的 forward
	doRaw(t, ts, http.MethodDelete, "/api/forwards/"+fwd.ID, bearerB, "", http.StatusNotFound)

	// A 自己能删
	doRaw(t, ts, http.MethodDelete, "/api/forwards/"+fwd.ID, bearerA, "", http.StatusNoContent)
}

func TestAdminEndpoints_AuthAndListings(t *testing.T) {
	ts, srv := newTestServer(t)

	// 没 token 应 401
	doRaw(t, ts, http.MethodGet, "/api/admin/info", "", "", http.StatusUnauthorized)

	// 创建一个 admin token，直接写 DB（CLI 流程的内部等价）
	rawToken := "test-admin-token-xyz"
	if err := srv.store.CreateAdminToken(t.Context(), store.AdminToken{
		ID:        "at-1",
		TokenHash: auth.HashSecret(rawToken),
		Label:     "test",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	// 错误 token 仍 401
	doRaw(t, ts, http.MethodGet, "/api/admin/info", "Bearer wrong", "", http.StatusUnauthorized)

	bearer := "Bearer " + rawToken

	// 准备一些数据：两个设备 + B 的 service + A 的 forward
	tA := mustDoJSON[proto.IssueTokenResponse](t, ts, http.MethodPost, "/api/enroll/tokens", "", "", http.StatusCreated)
	tB := mustDoJSON[proto.IssueTokenResponse](t, ts, http.MethodPost, "/api/enroll/tokens", "", "", http.StatusCreated)
	a := mustDoJSON[proto.EnrollDeviceResponse](t, ts, http.MethodPost, "/api/devices/enroll", "",
		toJSON(t, proto.EnrollDeviceRequest{Token: tA.Token, DeviceName: "alpha", SSHPubkey: testSSHPubkey}), http.StatusCreated)
	b := mustDoJSON[proto.EnrollDeviceResponse](t, ts, http.MethodPost, "/api/devices/enroll", "",
		toJSON(t, proto.EnrollDeviceRequest{Token: tB.Token, DeviceName: "beta", SSHPubkey: testSSHPubkey}), http.StatusCreated)
	bearerB := "Bearer " + b.DeviceID + "." + b.TunnelSecret
	svc := mustDoJSON[proto.ServiceDTO](t, ts, http.MethodPost, "/api/services", bearerB,
		toJSON(t, proto.AddServiceRequest{Name: "ssh", Protocol: "tcp", LocalAddr: "127.0.0.1:22"}),
		http.StatusCreated)
	bearerA := "Bearer " + a.DeviceID + "." + a.TunnelSecret
	mustDoJSON[proto.ForwardDTO](t, ts, http.MethodPost, "/api/forwards", bearerA,
		toJSON(t, proto.AddForwardRequest{RemoteServiceID: svc.ID, LocalPort: 8022}),
		http.StatusCreated)

	// info
	info := mustDoJSON[proto.AdminInfoResponse](t, ts, http.MethodGet, "/api/admin/info", bearer, "", http.StatusOK)
	if info.ServerFingerprint == "" || info.PortPoolMin == 0 {
		t.Errorf("AdminInfo 不完整: %+v", info)
	}

	devs := mustDoJSON[proto.AdminListDevicesResponse](t, ts, http.MethodGet, "/api/admin/devices", bearer, "", http.StatusOK)
	if len(devs.Devices) != 2 {
		t.Errorf("devices: %d", len(devs.Devices))
	}
	for _, d := range devs.Devices {
		if d.Name == "beta" && d.ServicesCount != 1 {
			t.Errorf("beta 应有 1 个 service, got %d", d.ServicesCount)
		}
		if d.Name == "alpha" && d.ForwardsCount != 1 {
			t.Errorf("alpha 应有 1 个 forward, got %d", d.ForwardsCount)
		}
	}

	svcs := mustDoJSON[proto.AdminListServicesResponse](t, ts, http.MethodGet, "/api/admin/services", bearer, "", http.StatusOK)
	if len(svcs.Services) != 1 || svcs.Services[0].DeviceName != "beta" || svcs.Services[0].LocalAddr != "127.0.0.1:22" {
		t.Errorf("services: %+v", svcs.Services)
	}

	fwds := mustDoJSON[proto.AdminListForwardsResponse](t, ts, http.MethodGet, "/api/admin/forwards", bearer, "", http.StatusOK)
	if len(fwds.Forwards) != 1 ||
		fwds.Forwards[0].OwnerDeviceName != "alpha" ||
		fwds.Forwards[0].RemoteDeviceName != "beta" {
		t.Errorf("forwards: %+v", fwds.Forwards)
	}

	// cookie 形式同样应通过
	cookieReq, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/admin/info", nil)
	cookieReq.AddCookie(&http.Cookie{Name: "oml_admin", Value: rawToken})
	resp, err := ts.Client().Do(cookieReq)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("cookie 认证失败: %d", resp.StatusCode)
	}
}

func TestAuthLogin_Flow(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := t.Context()

	// 先在 DB 直接造一个用户（模拟 CLI 的 admin user set 之后的状态）
	hash, err := auth.HashPassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.CreateAdminUser(ctx, store.AdminUser{
		ID: "u1", Username: "alice", PasswordHash: hash,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	// 错误密码 → 401
	doRaw(t, ts, http.MethodPost, "/api/auth/login", "",
		toJSON(t, proto.LoginRequest{Username: "alice", Password: "wrong"}),
		http.StatusUnauthorized)

	// 不存在用户 → 也 401（不区分，防枚举）
	doRaw(t, ts, http.MethodPost, "/api/auth/login", "",
		toJSON(t, proto.LoginRequest{Username: "bob", Password: "hunter2"}),
		http.StatusUnauthorized)

	// 正确登录 → 拿到 session token
	resp := mustDoJSON[proto.LoginResponse](t, ts, http.MethodPost, "/api/auth/login", "",
		toJSON(t, proto.LoginRequest{Username: "alice", Password: "hunter2"}),
		http.StatusOK)
	if resp.SessionToken == "" || !strings.HasPrefix(resp.SessionToken, "sess_") {
		t.Fatalf("session token 不对: %q", resp.SessionToken)
	}
	if resp.User.Username != "alice" {
		t.Errorf("user 不对: %+v", resp.User)
	}

	bearer := "Bearer " + resp.SessionToken

	// 用 session 调 admin API 成功
	mustDoJSON[proto.AdminInfoResponse](t, ts, http.MethodGet, "/api/admin/info", bearer, "", http.StatusOK)
	// /api/auth/me 返回当前用户
	me := mustDoJSON[proto.MeResponse](t, ts, http.MethodGet, "/api/auth/me", bearer, "", http.StatusOK)
	if me.User.Username != "alice" {
		t.Errorf("/me 返回错误: %+v", me)
	}

	// audit 应该有 auth.login 记录
	a := mustDoJSON[proto.AdminListAuditResponse](t, ts, http.MethodGet, "/api/admin/audit", bearer, "", http.StatusOK)
	loginSeen := false
	for _, e := range a.Entries {
		if e.Action == "auth.login" && e.Actor == "user:alice" {
			loginSeen = true
		}
	}
	if !loginSeen {
		t.Errorf("login audit not seen")
	}

	// logout
	doRaw(t, ts, http.MethodPost, "/api/auth/logout", bearer, "", http.StatusNoContent)
	// 登出后再用 token 应 401
	doRaw(t, ts, http.MethodGet, "/api/admin/info", bearer, "", http.StatusUnauthorized)
}

func TestAuthSession_ExpiredRejected(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := t.Context()

	// 直接造一个已过期 session
	hash, _ := auth.HashPassword("x")
	_ = srv.store.CreateAdminUser(ctx, store.AdminUser{
		ID: "u", Username: "u", PasswordHash: hash, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	raw := "sess_expired-test"
	_ = srv.store.CreateSession(ctx, store.Session{
		ID: "s", UserID: "u", TokenHash: auth.HashSecret(raw),
		CreatedAt: time.Now().Add(-2 * time.Hour),
		ExpiresAt: time.Now().Add(-time.Hour),
	})

	doRaw(t, ts, http.MethodGet, "/api/admin/info", "Bearer "+raw, "", http.StatusUnauthorized)
}

func TestAuthAdminTokenStillWorks(t *testing.T) {
	// 老的 admin_token 路径在新中间件下仍应工作（机器对机器场景）
	ts, srv := newTestServer(t)
	rawToken := "test-admin-still-works"
	if err := srv.store.CreateAdminToken(t.Context(), store.AdminToken{
		ID: "at", TokenHash: auth.HashSecret(rawToken), Label: "ci", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	mustDoJSON[proto.AdminInfoResponse](t, ts, http.MethodGet, "/api/admin/info",
		"Bearer "+rawToken, "", http.StatusOK)
}

func TestAdminMetricsAndAudit(t *testing.T) {
	ts, srv := newTestServer(t)
	rawToken := "test-admin-metric"
	if err := srv.store.CreateAdminToken(t.Context(), store.AdminToken{
		ID: "atm", TokenHash: auth.HashSecret(rawToken), Label: "m", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	bearer := "Bearer " + rawToken

	// 通过 admin API 创建一个 device + service，触发 audit 写入
	tok := mustDoJSON[proto.IssueTokenResponse](t, ts, http.MethodPost, "/api/admin/enroll/tokens", bearer, "", http.StatusCreated)
	dev := mustDoJSON[proto.EnrollDeviceResponse](t, ts, http.MethodPost, "/api/devices/enroll", "",
		toJSON(t, proto.EnrollDeviceRequest{Token: tok.Token, DeviceName: "m-dev", SSHPubkey: testSSHPubkey}), http.StatusCreated)
	svc := mustDoJSON[proto.ServiceDTO](t, ts, http.MethodPost, "/api/admin/services", bearer,
		toJSON(t, proto.AdminAddServiceRequest{DeviceID: dev.DeviceID, Name: "ssh", Protocol: "tcp", LocalAddr: "127.0.0.1:22"}),
		http.StatusCreated)

	// metrics 端点
	m := mustDoJSON[proto.AdminMetricsResponse](t, ts, http.MethodGet, "/api/admin/metrics", bearer, "", http.StatusOK)
	if m.DevicesTotal != 1 {
		t.Errorf("DevicesTotal: %d", m.DevicesTotal)
	}
	if m.ServicesTotal != 1 || m.ServicesEnabled != 1 {
		t.Errorf("Services counts off: %+v", m)
	}
	if m.AdminTokensTotal != 1 {
		t.Errorf("AdminTokensTotal: %d", m.AdminTokensTotal)
	}
	if m.PortPoolUsed != 1 {
		t.Errorf("PortPoolUsed: %d", m.PortPoolUsed)
	}
	if m.PortPoolSize <= 0 {
		t.Errorf("PortPoolSize: %d", m.PortPoolSize)
	}

	// audit 至少有 enroll + token issue + service add 三条
	a := mustDoJSON[proto.AdminListAuditResponse](t, ts, http.MethodGet, "/api/admin/audit", bearer, "", http.StatusOK)
	if len(a.Entries) < 3 {
		t.Errorf("expect >=3 audit entries, got %d", len(a.Entries))
	}
	found := map[string]bool{}
	for _, e := range a.Entries {
		found[e.Action] = true
	}
	for _, want := range []string{"device.enroll", "enroll_token.issue", "service.add"} {
		if !found[want] {
			t.Errorf("缺少 audit action: %s", want)
		}
	}

	// 触发 revoke device 看 audit 是否新增
	doRaw(t, ts, http.MethodPost, "/api/admin/devices/"+dev.DeviceID+"/revoke", bearer, "", http.StatusNoContent)
	a2 := mustDoJSON[proto.AdminListAuditResponse](t, ts, http.MethodGet, "/api/admin/audit?limit=50", bearer, "", http.StatusOK)
	revokeSeen := false
	for _, e := range a2.Entries {
		if e.Action == "device.revoke" && e.Target == dev.DeviceID {
			revokeSeen = true
		}
	}
	if !revokeSeen {
		t.Errorf("revoke audit entry not seen")
	}
	_ = svc // 仅占位
}

func TestAdminWriteEndpoints(t *testing.T) {
	ts, srv := newTestServer(t)
	rawToken := "test-admin-write"
	if err := srv.store.CreateAdminToken(t.Context(), store.AdminToken{
		ID: "at", TokenHash: auth.HashSecret(rawToken), Label: "w", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	bearer := "Bearer " + rawToken

	// 通过 admin API 生成 enrollment token
	tok := mustDoJSON[proto.IssueTokenResponse](t, ts, http.MethodPost, "/api/admin/enroll/tokens", bearer, "", http.StatusCreated)
	if tok.Token == "" {
		t.Fatal("admin issue token 空")
	}
	// 注册一个设备
	dev := mustDoJSON[proto.EnrollDeviceResponse](t, ts, http.MethodPost, "/api/devices/enroll", "",
		toJSON(t, proto.EnrollDeviceRequest{Token: tok.Token, DeviceName: "w-dev", SSHPubkey: testSSHPubkey}), http.StatusCreated)

	// admin 代发服务
	svc := mustDoJSON[proto.ServiceDTO](t, ts, http.MethodPost, "/api/admin/services", bearer,
		toJSON(t, proto.AdminAddServiceRequest{
			DeviceID: dev.DeviceID, Name: "svc-admin", Protocol: "tcp", LocalAddr: "127.0.0.1:1",
		}), http.StatusCreated)
	if svc.DeviceID != dev.DeviceID {
		t.Errorf("ownership 错: %+v", svc)
	}

	// admin disable / enable
	d := mustDoJSON[proto.ServiceDTO](t, ts, http.MethodPost, "/api/admin/services/"+svc.ID+"/disable", bearer, "", http.StatusOK)
	if d.Enabled {
		t.Error("admin disable 失败")
	}
	e := mustDoJSON[proto.ServiceDTO](t, ts, http.MethodPost, "/api/admin/services/"+svc.ID+"/enable", bearer, "", http.StatusOK)
	if !e.Enabled {
		t.Error("admin enable 失败")
	}

	// 第二个设备 + admin 代加 forward
	tok2 := mustDoJSON[proto.IssueTokenResponse](t, ts, http.MethodPost, "/api/admin/enroll/tokens", bearer, "", http.StatusCreated)
	dev2 := mustDoJSON[proto.EnrollDeviceResponse](t, ts, http.MethodPost, "/api/devices/enroll", "",
		toJSON(t, proto.EnrollDeviceRequest{Token: tok2.Token, DeviceName: "w-dev2", SSHPubkey: testSSHPubkey}), http.StatusCreated)
	fwd := mustDoJSON[proto.ForwardDTO](t, ts, http.MethodPost, "/api/admin/forwards", bearer,
		toJSON(t, proto.AdminAddForwardRequest{
			OwnerDeviceID: dev2.DeviceID, RemoteServiceID: svc.ID, LocalPort: 8089,
		}), http.StatusCreated)
	if fwd.OwnerDeviceID != dev2.DeviceID {
		t.Errorf("forward owner 错: %+v", fwd)
	}

	// admin disable forward → bootstrap 不再包含
	df := mustDoJSON[proto.ForwardDTO](t, ts, http.MethodPost, "/api/admin/forwards/"+fwd.ID+"/disable", bearer, "", http.StatusOK)
	if df.Enabled {
		t.Error("admin disable forward 失败")
	}
	bearerDev2 := "Bearer " + dev2.DeviceID + "." + dev2.TunnelSecret
	boot := mustDoJSON[proto.BootstrapResponse](t, ts, http.MethodGet, "/api/devices/me/bootstrap", bearerDev2, "", http.StatusOK)
	if len(boot.Locals) != 0 {
		t.Errorf("disabled forward 不应在 bootstrap, got %d", len(boot.Locals))
	}

	// admin 删除 service / forward
	doRaw(t, ts, http.MethodDelete, "/api/admin/forwards/"+fwd.ID, bearer, "", http.StatusNoContent)
	doRaw(t, ts, http.MethodDelete, "/api/admin/services/"+svc.ID, bearer, "", http.StatusNoContent)

	// admin revoke device：DB 删 + chisel UserIndex 移除
	// 先注入 chisel user 以验证 RemoveDevice 被调用（chisel server 用 in-memory user index）
	if err := srv.tunnel.AddDevice(dev.DeviceID, dev.TunnelSecret); err != nil {
		t.Fatal(err)
	}
	doRaw(t, ts, http.MethodPost, "/api/admin/devices/"+dev.DeviceID+"/revoke", bearer, "", http.StatusNoContent)
	// 二次 revoke 应 404
	doRaw(t, ts, http.MethodPost, "/api/admin/devices/"+dev.DeviceID+"/revoke", bearer, "", http.StatusNotFound)

	// admin services 列表应不再含 svc（已删）和 dev 的服务（已 cascade）
	admList := mustDoJSON[proto.AdminListServicesResponse](t, ts, http.MethodGet, "/api/admin/services", bearer, "", http.StatusOK)
	for _, s := range admList.Services {
		if s.DeviceID == dev.DeviceID {
			t.Errorf("revoke 后不应仍出现该 device 的 service")
		}
	}
}

func TestUDPServiceFlow(t *testing.T) {
	ts, _ := newTestServer(t)

	tok := mustDoJSON[proto.IssueTokenResponse](t, ts, http.MethodPost, "/api/enroll/tokens", "", "", http.StatusCreated)
	dev := mustDoJSON[proto.EnrollDeviceResponse](t, ts, http.MethodPost, "/api/devices/enroll", "",
		toJSON(t, proto.EnrollDeviceRequest{Token: tok.Token, DeviceName: "udp-dev", SSHPubkey: testSSHPubkey}), http.StatusCreated)
	bearer := "Bearer " + dev.DeviceID + "." + dev.TunnelSecret

	// UDP 服务能正常创建
	svc := mustDoJSON[proto.ServiceDTO](t, ts, http.MethodPost, "/api/services", bearer,
		toJSON(t, proto.AddServiceRequest{Name: "dns", Protocol: "udp", LocalAddr: "127.0.0.1:53"}),
		http.StatusCreated)
	if svc.Protocol != "udp" {
		t.Errorf("协议字段应为 udp, got %q", svc.Protocol)
	}

	// bootstrap 中协议正确流转，daemon 才能正确拼出 .../udp 后缀的 chisel spec
	boot := mustDoJSON[proto.BootstrapResponse](t, ts, http.MethodGet, "/api/devices/me/bootstrap", bearer, "", http.StatusOK)
	if len(boot.Remotes) != 1 || boot.Remotes[0].Protocol != "udp" {
		t.Errorf("bootstrap.Remotes 协议不对: %+v", boot.Remotes)
	}

	// 非法协议被拒
	doRaw(t, ts, http.MethodPost, "/api/services", bearer,
		toJSON(t, proto.AddServiceRequest{Name: "bad", Protocol: "sctp", LocalAddr: "127.0.0.1:1"}),
		http.StatusBadRequest)
}

func TestForwardRejectsNonExistentRemote(t *testing.T) {
	ts, _ := newTestServer(t)
	tA := mustDoJSON[proto.IssueTokenResponse](t, ts, http.MethodPost, "/api/enroll/tokens", "", "", http.StatusCreated)
	a := mustDoJSON[proto.EnrollDeviceResponse](t, ts, http.MethodPost, "/api/devices/enroll", "",
		toJSON(t, proto.EnrollDeviceRequest{Token: tA.Token, DeviceName: "A", SSHPubkey: testSSHPubkey}), http.StatusCreated)
	bearer := "Bearer " + a.DeviceID + "." + a.TunnelSecret

	doRaw(t, ts, http.MethodPost, "/api/forwards", bearer,
		toJSON(t, proto.AddForwardRequest{RemoteServiceID: "nope", LocalPort: 8022}),
		http.StatusNotFound)
}

func TestCrossDeviceDeleteReturns404(t *testing.T) {
	ts, _ := newTestServer(t)
	// 注册两个设备 A, B；A 创建服务，用 B 的 bearer 尝试删除
	tok1 := mustDoJSON[proto.IssueTokenResponse](t, ts, http.MethodPost, "/api/enroll/tokens", "", "", http.StatusCreated)
	tok2 := mustDoJSON[proto.IssueTokenResponse](t, ts, http.MethodPost, "/api/enroll/tokens", "", "", http.StatusCreated)

	a := mustDoJSON[proto.EnrollDeviceResponse](t, ts, http.MethodPost, "/api/devices/enroll", "",
		toJSON(t, proto.EnrollDeviceRequest{Token: tok1.Token, DeviceName: "a", SSHPubkey: testSSHPubkey}), http.StatusCreated)
	b := mustDoJSON[proto.EnrollDeviceResponse](t, ts, http.MethodPost, "/api/devices/enroll", "",
		toJSON(t, proto.EnrollDeviceRequest{Token: tok2.Token, DeviceName: "b", SSHPubkey: testSSHPubkey}), http.StatusCreated)

	bearerA := "Bearer " + a.DeviceID + "." + a.TunnelSecret
	bearerB := "Bearer " + b.DeviceID + "." + b.TunnelSecret

	svc := mustDoJSON[proto.ServiceDTO](t, ts, http.MethodPost, "/api/services", bearerA,
		toJSON(t, proto.AddServiceRequest{Name: "ssh", Protocol: "tcp", LocalAddr: "127.0.0.1:22"}),
		http.StatusCreated)

	doRaw(t, ts, http.MethodDelete, "/api/services/"+svc.ID, bearerB, "", http.StatusNotFound)
}

// ---------------- helpers ----------------

func toJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func doRaw(t *testing.T, ts *httptest.Server, method, path, bearer, body string, wantStatus int) string {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", bearer)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s 状态=%d 期望=%d body=%s", method, path, resp.StatusCode, wantStatus, string(bodyBytes))
	}
	return string(bodyBytes)
}

func mustDoJSON[T any](t *testing.T, ts *httptest.Server, method, path, bearer, body string, wantStatus int) T {
	t.Helper()
	raw := doRaw(t, ts, method, path, bearer, body, wantStatus)
	var out T
	if raw == "" {
		return out
	}
	if err := json.NewDecoder(bytes.NewReader([]byte(raw))).Decode(&out); err != nil {
		t.Fatalf("解析响应 %s 失败: %v\nraw=%s", path, err, raw)
	}
	return out
}
