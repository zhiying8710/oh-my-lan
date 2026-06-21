package server

import (
	"net/http"
	"testing"

	"github.com/zhiying8710/oh-my-lan/internal/proto"
)

// admin handler 的错误分支：未覆盖时是 0%，覆盖后覆盖率显著上升，且作为"反例文档"
// 让别人知道这些校验真的存在。

func TestAdminCreateService_BadJSON(t *testing.T) {
	ts, srv := newTestServer(t)
	bearer := newAdminBearer(t, srv)
	doRaw(t, ts, http.MethodPost, "/api/admin/services", bearer, "not-json{", http.StatusBadRequest)
}

func TestAdminCreateService_MissingFields(t *testing.T) {
	ts, srv := newTestServer(t)
	bearer := newAdminBearer(t, srv)
	// device_id 缺失
	doRaw(t, ts, http.MethodPost, "/api/admin/services", bearer,
		toJSON(t, proto.AdminAddServiceRequest{Name: "ssh", Protocol: "tcp", LocalAddr: "127.0.0.1:22"}),
		http.StatusBadRequest)
	// name 缺失
	doRaw(t, ts, http.MethodPost, "/api/admin/services", bearer,
		toJSON(t, proto.AdminAddServiceRequest{DeviceID: "x", Protocol: "tcp", LocalAddr: "127.0.0.1:22"}),
		http.StatusBadRequest)
	// local_addr 缺失
	doRaw(t, ts, http.MethodPost, "/api/admin/services", bearer,
		toJSON(t, proto.AdminAddServiceRequest{DeviceID: "x", Name: "ssh", Protocol: "tcp"}),
		http.StatusBadRequest)
}

func TestAdminCreateService_BadProtocol(t *testing.T) {
	ts, srv := newTestServer(t)
	bearer := newAdminBearer(t, srv)
	doRaw(t, ts, http.MethodPost, "/api/admin/services", bearer,
		toJSON(t, proto.AdminAddServiceRequest{
			DeviceID: "x", Name: "ssh", Protocol: "sctp", LocalAddr: "127.0.0.1:22",
		}),
		http.StatusBadRequest)
}

func TestAdminCreateService_DeviceNotFound(t *testing.T) {
	ts, srv := newTestServer(t)
	bearer := newAdminBearer(t, srv)
	doRaw(t, ts, http.MethodPost, "/api/admin/services", bearer,
		toJSON(t, proto.AdminAddServiceRequest{
			DeviceID: "no-such-device", Name: "ssh", Protocol: "tcp", LocalAddr: "127.0.0.1:22",
		}),
		http.StatusNotFound)
}

func TestAdminCreateForward_BadJSON(t *testing.T) {
	ts, srv := newTestServer(t)
	bearer := newAdminBearer(t, srv)
	doRaw(t, ts, http.MethodPost, "/api/admin/forwards", bearer, "{bad json", http.StatusBadRequest)
}

func TestAdminCreateForward_OwnerNotFound(t *testing.T) {
	ts, srv := newTestServer(t)
	bearer := newAdminBearer(t, srv)
	// remote_service 也不存在——按 handler 实现的校验顺序可能先报哪个，统一接受 400/404
	resp := doRaw(t, ts, http.MethodPost, "/api/admin/forwards", bearer,
		toJSON(t, proto.AdminAddForwardRequest{
			OwnerDeviceID: "nope", RemoteServiceID: "x", LocalPort: 8022,
		}),
		http.StatusNotFound)
	if resp == "" {
		t.Errorf("应返回错误信息")
	}
}

func TestAdminServiceItem_NotFound(t *testing.T) {
	ts, srv := newTestServer(t)
	bearer := newAdminBearer(t, srv)
	doRaw(t, ts, http.MethodPost, "/api/admin/services/missing/enable", bearer, "", http.StatusNotFound)
	doRaw(t, ts, http.MethodPost, "/api/admin/services/missing/disable", bearer, "", http.StatusNotFound)
	doRaw(t, ts, http.MethodDelete, "/api/admin/services/missing", bearer, "", http.StatusNotFound)
}

func TestAdminForwardItem_NotFound(t *testing.T) {
	ts, srv := newTestServer(t)
	bearer := newAdminBearer(t, srv)
	doRaw(t, ts, http.MethodPost, "/api/admin/forwards/missing/enable", bearer, "", http.StatusNotFound)
	doRaw(t, ts, http.MethodDelete, "/api/admin/forwards/missing", bearer, "", http.StatusNotFound)
}

func TestAdminDeviceItem_NotFound(t *testing.T) {
	ts, srv := newTestServer(t)
	bearer := newAdminBearer(t, srv)
	doRaw(t, ts, http.MethodPost, "/api/admin/devices/missing/revoke", bearer, "", http.StatusNotFound)
	doRaw(t, ts, http.MethodPost, "/api/admin/devices/missing/kick", bearer, "", http.StatusNotFound)
}

// kick：device 存在 → 204；之后 device 在 chisel UserIndex 仍可用（这是与 revoke 的关键区别——
// 仅 1s 软重置，不删数据）。
func TestAdminDeviceKick(t *testing.T) {
	ts, srv := newTestServer(t)
	bearer := newAdminBearer(t, srv)
	tok := mustDoJSON[proto.IssueTokenResponse](t, ts, http.MethodPost, "/api/enroll/tokens", "", "", http.StatusCreated)
	dev := mustDoJSON[proto.EnrollDeviceResponse](t, ts, http.MethodPost, "/api/devices/enroll", "",
		toJSON(t, proto.EnrollDeviceRequest{Token: tok.Token, DeviceName: "kicktest", SSHPubkey: testSSHPubkey}), http.StatusCreated)

	if err := srv.tunnel.AddDevice(dev.DeviceID, dev.TunnelSecret); err != nil {
		t.Fatal(err)
	}
	doRaw(t, ts, http.MethodPost, "/api/admin/devices/"+dev.DeviceID+"/kick", bearer, "", http.StatusNoContent)

	// 走完 kick 后 device 仍在 DB——bootstrap 仍可用（注：device.DeleteDevice 没被调）
	bearerDev := "Bearer " + dev.DeviceID + "." + dev.TunnelSecret
	doRaw(t, ts, http.MethodGet, "/api/devices/me/bootstrap", bearerDev, "", http.StatusOK)

	// audit 应有 device.kick 条目
	a := mustDoJSON[proto.AdminListAuditResponse](t, ts, http.MethodGet, "/api/admin/audit?limit=20", bearer, "", http.StatusOK)
	kickSeen := false
	for _, e := range a.Entries {
		if e.Action == "device.kick" && e.Target == dev.DeviceID {
			kickSeen = true
		}
	}
	if !kickSeen {
		t.Errorf("audit 中应有 device.kick 条目")
	}

	// 仅 GET/PUT/DELETE 等非 POST 应 405
	doRaw(t, ts, http.MethodGet, "/api/admin/devices/"+dev.DeviceID+"/kick", bearer, "", http.StatusMethodNotAllowed)
}

// 并发 kick + revoke 同 device 不能在 chisel UserIndex 留 phantom user。
// 历史 bug：kick 中间 sleep 1s 期间 revoke 进入，kick 醒来后 AddDevice，DB 里
// 已无该 device 但 chisel 仍认这个 user → 攻击者用旧 tunnel_secret 跑 chisel
// L-spec 借 VPS 做内网跳板。修：kick 改原子 Remove+Add 不再 sleep。
func TestAdminDeviceKick_RaceWithRevoke(t *testing.T) {
	ts, srv := newTestServer(t)
	bearer := newAdminBearer(t, srv)

	for i := 0; i < 20; i++ {
		tok := mustDoJSON[proto.IssueTokenResponse](t, ts, http.MethodPost, "/api/enroll/tokens", "", "", http.StatusCreated)
		dev := mustDoJSON[proto.EnrollDeviceResponse](t, ts, http.MethodPost, "/api/devices/enroll", "",
			toJSON(t, proto.EnrollDeviceRequest{Token: tok.Token, DeviceName: "race", SSHPubkey: testSSHPubkey}), http.StatusCreated)
		_ = srv.tunnel.AddDevice(dev.DeviceID, dev.TunnelSecret)

		// 同时发 kick + revoke。两者顺序无所谓——最终态必须是 chisel UserIndex 不含该 user。
		done := make(chan struct{}, 2)
		go func() {
			doRawAny(t, ts, http.MethodPost, "/api/admin/devices/"+dev.DeviceID+"/kick", bearer, "")
			done <- struct{}{}
		}()
		go func() {
			doRawAny(t, ts, http.MethodPost, "/api/admin/devices/"+dev.DeviceID+"/revoke", bearer, "")
			done <- struct{}{}
		}()
		<-done
		<-done

		if srv.tunnel.HasDevice(dev.DeviceID) {
			t.Fatalf("iter %d: revoke 完成后 chisel UserIndex 不应含 %s（phantom user）", i, dev.DeviceID)
		}
	}
}
