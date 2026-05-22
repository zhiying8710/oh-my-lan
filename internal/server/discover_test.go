package server

import (
	"net/http"
	"testing"

	"github.com/zhiying8710/oh-my-lan/internal/proto"
)

// TestDiscover_FiltersSelfAndDisabled 覆盖 /api/devices/me/discover 的三个核心规则：
//  1. 排除调用者本机的服务（forward 到自己没意义）
//  2. 排除 disabled 服务（mesh 视角只关心当前可用的）
//  3. 返回的 ServiceBriefDTO 字段齐全（device_name 不能空——UI 用它做选项标签）
func TestDiscover_FiltersSelfAndDisabled(t *testing.T) {
	ts, _ := newTestServer(t)

	// 三台设备：A 是调用方；B/C 是发布者
	tA := mustDoJSON[proto.IssueTokenResponse](t, ts, http.MethodPost, "/api/enroll/tokens", "", "", http.StatusCreated)
	tB := mustDoJSON[proto.IssueTokenResponse](t, ts, http.MethodPost, "/api/enroll/tokens", "", "", http.StatusCreated)
	tC := mustDoJSON[proto.IssueTokenResponse](t, ts, http.MethodPost, "/api/enroll/tokens", "", "", http.StatusCreated)
	a := mustDoJSON[proto.EnrollDeviceResponse](t, ts, http.MethodPost, "/api/devices/enroll", "",
		toJSON(t, proto.EnrollDeviceRequest{Token: tA.Token, DeviceName: "alpha"}), http.StatusCreated)
	b := mustDoJSON[proto.EnrollDeviceResponse](t, ts, http.MethodPost, "/api/devices/enroll", "",
		toJSON(t, proto.EnrollDeviceRequest{Token: tB.Token, DeviceName: "beta"}), http.StatusCreated)
	c := mustDoJSON[proto.EnrollDeviceResponse](t, ts, http.MethodPost, "/api/devices/enroll", "",
		toJSON(t, proto.EnrollDeviceRequest{Token: tC.Token, DeviceName: "gamma"}), http.StatusCreated)
	bearerA := "Bearer " + a.DeviceID + "." + a.TunnelSecret
	bearerB := "Bearer " + b.DeviceID + "." + b.TunnelSecret
	bearerC := "Bearer " + c.DeviceID + "." + c.TunnelSecret

	// A 发布一个服务（应被自己 discover 过滤掉）
	mustDoJSON[proto.ServiceDTO](t, ts, http.MethodPost, "/api/services", bearerA,
		toJSON(t, proto.AddServiceRequest{Name: "self-svc", Protocol: "tcp", LocalAddr: "127.0.0.1:8080"}),
		http.StatusCreated)
	// B 发布一个 enabled 服务
	bsvc := mustDoJSON[proto.ServiceDTO](t, ts, http.MethodPost, "/api/services", bearerB,
		toJSON(t, proto.AddServiceRequest{Name: "ssh", Protocol: "tcp", LocalAddr: "127.0.0.1:22"}),
		http.StatusCreated)
	// C 发布两个：一个 enabled、一个稍后 disabled
	cActive := mustDoJSON[proto.ServiceDTO](t, ts, http.MethodPost, "/api/services", bearerC,
		toJSON(t, proto.AddServiceRequest{Name: "dns", Protocol: "udp", LocalAddr: "127.0.0.1:53"}),
		http.StatusCreated)
	cDead := mustDoJSON[proto.ServiceDTO](t, ts, http.MethodPost, "/api/services", bearerC,
		toJSON(t, proto.AddServiceRequest{Name: "old", Protocol: "tcp", LocalAddr: "127.0.0.1:9999"}),
		http.StatusCreated)
	mustDoJSON[proto.ServiceDTO](t, ts, http.MethodPost, "/api/services/"+cDead.ID+"/disable", bearerC, "", http.StatusOK)

	// A 调 /discover：应见 bsvc + cActive，未见 self-svc / cDead
	got := mustDoJSON[proto.DiscoverDTO](t, ts, http.MethodGet, "/api/devices/me/discover", bearerA, "", http.StatusOK)
	if len(got.Services) != 2 {
		t.Fatalf("expect 2 services, got %d: %+v", len(got.Services), got.Services)
	}
	byID := map[string]proto.ServiceBriefDTO{}
	for _, s := range got.Services {
		byID[s.ID] = s
	}
	if _, ok := byID[bsvc.ID]; !ok {
		t.Errorf("应包含 B 的服务 %s", bsvc.ID)
	}
	if _, ok := byID[cActive.ID]; !ok {
		t.Errorf("应包含 C 的 enabled 服务 %s", cActive.ID)
	}
	if _, ok := byID[cDead.ID]; ok {
		t.Errorf("不应包含 disabled 服务 %s", cDead.ID)
	}

	// device_name 字段必须填——UI 用它做选项标签，缺了会显示空白下拉
	if byID[bsvc.ID].DeviceName != "beta" {
		t.Errorf("DeviceName beta，got %q", byID[bsvc.ID].DeviceName)
	}
	if byID[cActive.ID].DeviceName != "gamma" {
		t.Errorf("DeviceName gamma，got %q", byID[cActive.ID].DeviceName)
	}

	// UDP 服务应保留 protocol 字段
	if byID[cActive.ID].Protocol != "udp" {
		t.Errorf("UDP 协议应原样保留, got %q", byID[cActive.ID].Protocol)
	}
}

// TestDiscover_EmptyMesh 单设备 mesh 返回空数组（而非 null），让前端 .map() 不爆。
func TestDiscover_EmptyMesh(t *testing.T) {
	ts, _ := newTestServer(t)
	tok := mustDoJSON[proto.IssueTokenResponse](t, ts, http.MethodPost, "/api/enroll/tokens", "", "", http.StatusCreated)
	d := mustDoJSON[proto.EnrollDeviceResponse](t, ts, http.MethodPost, "/api/devices/enroll", "",
		toJSON(t, proto.EnrollDeviceRequest{Token: tok.Token, DeviceName: "solo"}), http.StatusCreated)
	bearer := "Bearer " + d.DeviceID + "." + d.TunnelSecret

	got := mustDoJSON[proto.DiscoverDTO](t, ts, http.MethodGet, "/api/devices/me/discover", bearer, "", http.StatusOK)
	if got.Services == nil {
		t.Errorf("空 mesh 应返回 []，不能是 null（前端会 .map() 失败）")
	}
	if len(got.Services) != 0 {
		t.Errorf("空 mesh 应为 0 条，got %d", len(got.Services))
	}
}

// TestDiscover_RequiresDeviceAuth 没有 device bearer 应 401（admin token 也不行——
// /discover 是 device-视角端点）。
func TestDiscover_RequiresDeviceAuth(t *testing.T) {
	ts, _ := newTestServer(t)
	doRaw(t, ts, http.MethodGet, "/api/devices/me/discover", "", "", http.StatusUnauthorized)
	doRaw(t, ts, http.MethodGet, "/api/devices/me/discover", "Bearer junk.token", "", http.StatusUnauthorized)
}
