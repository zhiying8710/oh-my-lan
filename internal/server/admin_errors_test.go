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
}
