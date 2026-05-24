// Package device 实现走 device bearer 认证（或本机/公开）的 HTTP handler——
// /api/enroll/tokens、/api/devices/enroll、/api/services*、/api/forwards*、
// /api/devices/me/{bootstrap,discover}、/api/services/all。
//
// Handler 持有自己的依赖，由 server orchestrator 在 router.go 构造并 wire 路由。
// 共享工具（WriteJSON / DTO mapper / ctx keys / Audit）都在 internal/server/api/。
package device

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/zhiying8710/oh-my-lan/internal/auth"
	"github.com/zhiying8710/oh-my-lan/internal/enroll"
	"github.com/zhiying8710/oh-my-lan/internal/proto"
	"github.com/zhiying8710/oh-my-lan/internal/server/api"
	"github.com/zhiying8710/oh-my-lan/internal/server/sshacct"
	"github.com/zhiying8710/oh-my-lan/internal/store"
	"github.com/zhiying8710/oh-my-lan/internal/tunnel"
)

// Handler 是 device 子包的入口对象，承载所有依赖。
type Handler struct {
	Store               *store.Store
	Tunnel              *tunnel.Server
	Ports               *api.PortAllocator
	Enroll              *enroll.Service
	Auditor             *api.Auditor
	Logger              *slog.Logger
	ChiselAdvertiseAddr string
	// SSH 跳板：nil 表示测试场景（不动 /etc/passwd）
	SSHAcct *sshacct.Manager
	SSHHost string // enroll 响应里给客户端的 ssh host
	SSHPort int    // enroll 响应里给客户端的 ssh port
}

// deviceFromContext 从 r.Context() 拿 store.Device。middleware（api.AuthDeviceMiddleware）
// 已在路由 wire 时注入。
func deviceFromContext(r *http.Request) (store.Device, bool) {
	return api.DeviceFromContext(r.Context())
}

// POST /api/enroll/tokens  (local only)
func (h *Handler) HandleIssueToken(w http.ResponseWriter, r *http.Request) {
	var req proto.IssueTokenRequest
	if r.ContentLength > 0 {
		if err := api.DecodeJSON(r, &req); err != nil {
			api.WriteError(w, http.StatusBadRequest, "请求体非法 JSON: "+err.Error())
			return
		}
	}
	ttl := time.Duration(req.TTLSeconds) * time.Second
	issued, err := h.Enroll.IssueToken(r.Context(), ttl)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	api.WriteJSON(w, http.StatusCreated, proto.IssueTokenResponse{
		ID:        issued.ID,
		Token:     issued.Token,
		ExpiresAt: issued.ExpiresAt,
	})
}

// POST /api/devices/enroll
func (h *Handler) HandleEnrollDevice(w http.ResponseWriter, r *http.Request) {
	var req proto.EnrollDeviceRequest
	if err := api.DecodeJSON(r, &req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "请求体非法 JSON: "+err.Error())
		return
	}
	// 强制 SSH key——事故后不再支持"裸 enroll"。客户端自动生成 ed25519 + 上传公钥。
	if err := sshacct.ValidatePubkey(req.SSHPubkey); err != nil {
		api.WriteError(w, http.StatusBadRequest, "ssh_pubkey 必填且格式合法（ssh-ed25519 单行）: "+err.Error())
		return
	}

	dev, err := h.Enroll.EnrollDevice(r.Context(), req.Token, strings.TrimSpace(req.DeviceName))
	switch {
	case errors.Is(err, enroll.ErrTokenInvalid), errors.Is(err, enroll.ErrTokenExpired):
		api.WriteError(w, http.StatusUnauthorized, err.Error())
		return
	case errors.Is(err, enroll.ErrDeviceExists):
		api.WriteError(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		api.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	// 注册成功，把设备注入 chisel server 的 user index，让它能立刻拨号上来。
	if err := h.Tunnel.AddDevice(dev.DeviceID, dev.TunnelSecret); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "chisel 用户注册失败: "+err.Error())
		return
	}

	// VPS 端建受限 SSH 账号 + 写 authorized_keys。SSHAcct=nil 仅测试。
	var sshUsername string
	if h.SSHAcct != nil {
		u, err := h.SSHAcct.Provision(r.Context(), dev.DeviceID, req.SSHPubkey, nil)
		if err != nil {
			// rollback：刚 enroll 出来的 device 删掉，chisel user 也撤销
			h.Tunnel.RemoveDevice(dev.DeviceID)
			_ = h.Store.DeleteDevice(r.Context(), dev.DeviceID)
			api.WriteError(w, http.StatusInternalServerError, "provision SSH 账号失败: "+err.Error())
			return
		}
		sshUsername = u
	}

	// 把 SSH 信息持久化到 device row 上（DeviceID / TunnelSecret 已写）。
	// 注意 Enroll.EnrollDevice 是 CreateDevice，但没填 ssh_pubkey/ssh_username——
	// 这里走 UPDATE 补上。
	if _, err := h.Store.GetDeviceByID(r.Context(), dev.DeviceID); err == nil {
		// 走 raw exec：避免给 Store 加一个 SetDeviceSSH 方法仅一处用
		_ = updateDeviceSSH(r.Context(), h.Store, dev.DeviceID, req.SSHPubkey, sshUsername)
	}

	h.Auditor.Write(r.Context(), "system", api.ActionDeviceEnroll, dev.DeviceID,
		map[string]any{"device_name": dev.DeviceName, "ssh_username": sshUsername})

	api.WriteJSON(w, http.StatusCreated, proto.EnrollDeviceResponse{
		DeviceID:          dev.DeviceID,
		DeviceName:        dev.DeviceName,
		TunnelSecret:      dev.TunnelSecret,
		ServerFingerprint: h.Tunnel.Fingerprint(),
		ChiselAddr:        h.ChiselAdvertiseAddr,
		SSHUsername:       sshUsername,
		SSHHost:           h.SSHHost,
		SSHPort:           h.SSHPort,
	})
}

// updateDeviceSSH: 给 device 行补 ssh_pubkey / ssh_username。enroll 走 enroll.Service.EnrollDevice
// 内部 CreateDevice，没暴露 SSH 字段；这里 raw UPDATE 一次。
func updateDeviceSSH(ctx context.Context, st *store.Store, deviceID, pubkey, username string) error {
	return st.SetDeviceSSH(ctx, deviceID, pubkey, username)
}

// POST /api/services  (device auth)
func (h *Handler) HandleAddService(w http.ResponseWriter, r *http.Request) {
	dev, _ := deviceFromContext(r)
	var req proto.AddServiceRequest
	if err := api.DecodeJSON(r, &req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "请求体非法 JSON: "+err.Error())
		return
	}
	if req.Name == "" || req.LocalAddr == "" {
		api.WriteError(w, http.StatusBadRequest, "name / local_addr 必填")
		return
	}
	if req.Protocol != store.ProtocolTCP && req.Protocol != store.ProtocolUDP {
		api.WriteError(w, http.StatusBadRequest, "protocol 必须是 tcp 或 udp")
		return
	}
	localAddr, err := api.NormalizeLocalAddr(req.LocalAddr)
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.LocalAddr = localAddr

	port, err := h.Ports.Allocate(r.Context())
	if err != nil {
		api.WriteError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	id, err := auth.NewRandomID()
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// BindLocal 默认 true（事故后强制安全默认）。客户端必须显式 false 才能 0.0.0.0 暴露。
	bindLocal := true
	if req.BindLocal != nil {
		bindLocal = *req.BindLocal
	}
	svc := store.Service{
		ID:         id,
		DeviceID:   dev.ID,
		Name:       req.Name,
		Protocol:   req.Protocol,
		LocalAddr:  req.LocalAddr,
		PublicPort: port,
		Enabled:    true,
		BindLocal:  bindLocal,
		CreatedAt:  time.Now().UTC(),
	}
	if err := h.Store.CreateService(r.Context(), svc); err != nil {
		if store.IsUniqueViolation(err) {
			api.WriteError(w, http.StatusConflict, "服务名已存在或端口冲突")
			return
		}
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.Auditor.Write(r.Context(), api.DeviceActor(dev.ID), api.ActionServiceAdd, svc.ID,
		map[string]any{"name": svc.Name, "protocol": svc.Protocol, "public_port": svc.PublicPort, "bind_local": svc.BindLocal})
	api.WriteJSON(w, http.StatusCreated, api.ToServiceDTO(svc))
}

// GET /api/services  (device auth)
func (h *Handler) HandleListServices(w http.ResponseWriter, r *http.Request) {
	dev, _ := deviceFromContext(r)
	list, err := h.Store.ListServicesByDevice(r.Context(), dev.ID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := proto.ListServicesResponse{Services: make([]proto.ServiceDTO, 0, len(list))}
	for _, sv := range list {
		out.Services = append(out.Services, api.ToServiceDTO(sv))
	}
	api.WriteJSON(w, http.StatusOK, out)
}

// DELETE /api/services/{id}                (device auth)
// POST   /api/services/{id}/enable          (device auth)
// POST   /api/services/{id}/disable         (device auth)
func (h *Handler) HandleServiceItem(w http.ResponseWriter, r *http.Request) {
	dev, _ := deviceFromContext(r)

	rest := strings.TrimPrefix(r.URL.Path, "/api/services/")
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	if id == "" {
		api.WriteError(w, http.StatusBadRequest, "URL 中缺少 service id")
		return
	}
	var action string
	if len(parts) == 2 {
		action = parts[1]
	}

	svc, err := h.Store.GetService(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			api.WriteError(w, http.StatusNotFound, "服务不存在")
			return
		}
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if svc.DeviceID != dev.ID {
		// 跨设备访问返回 404 以避免泄漏 id 存在性
		api.WriteError(w, http.StatusNotFound, "服务不存在")
		return
	}

	switch {
	case action == "" && r.Method == http.MethodDelete:
		if err := h.Store.DeleteService(r.Context(), id); err != nil {
			api.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		h.Auditor.Write(r.Context(), api.DeviceActor(dev.ID), api.ActionServiceDelete, id, nil)
		w.WriteHeader(http.StatusNoContent)
	case action == "enable" && r.Method == http.MethodPost:
		if err := h.Store.SetServiceEnabled(r.Context(), id, true); err != nil {
			api.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		svc.Enabled = true
		h.Auditor.Write(r.Context(), api.DeviceActor(dev.ID), api.ActionServiceEnable, id, nil)
		api.WriteJSON(w, http.StatusOK, api.ToServiceDTO(svc))
	case action == "disable" && r.Method == http.MethodPost:
		if err := h.Store.SetServiceEnabled(r.Context(), id, false); err != nil {
			api.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		svc.Enabled = false
		h.Auditor.Write(r.Context(), api.DeviceActor(dev.ID), api.ActionServiceDisable, id, nil)
		api.WriteJSON(w, http.StatusOK, api.ToServiceDTO(svc))
	default:
		api.WriteError(w, http.StatusMethodNotAllowed, "不支持的方法或路径")
	}
}

// GET /api/devices/me/bootstrap  (device auth)
//
// 同时充当设备 keepalive：每次调用更新 last_seen_at + status=online。
// 客户端 daemon 默认每 30s 调一次（M2.1 reload watcher）。
// 内部用 JOIN 查询避免 N+1：services + forwards 各一次 SQL。
func (h *Handler) HandleBootstrap(w http.ResponseWriter, r *http.Request) {
	dev, _ := deviceFromContext(r)
	if err := h.Store.UpdateDeviceStatus(r.Context(), dev.ID, store.DeviceStatusOnline, time.Now()); err != nil {
		// keepalive 失败不阻断业务，记日志继续
		h.Logger.Warn("更新设备 last_seen 失败", "device", dev.ID, "err", err)
	}

	myServices, err := h.Store.ListServicesByDevice(r.Context(), dev.ID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	myForwards, err := h.Store.ListForwardsJoined(r.Context(), dev.ID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := proto.BootstrapResponse{
		ServerFingerprint: h.Tunnel.Fingerprint(),
		ChiselAddr:        h.ChiselAdvertiseAddr,
		Remotes:           make([]proto.RemoteEntry, 0, len(myServices)),
		Locals:            make([]proto.LocalEntry, 0, len(myForwards)),
	}
	for _, sv := range myServices {
		if !sv.Enabled {
			continue
		}
		out.Remotes = append(out.Remotes, proto.RemoteEntry{
			ServiceID:  sv.ID,
			PublicPort: sv.PublicPort,
			LocalAddr:  sv.LocalAddr,
			Protocol:   sv.Protocol,
			BindLocal:  sv.BindLocal,
		})
	}
	for _, f := range myForwards {
		if !f.Enabled || !f.RemoteServiceEnabled || f.RemoteServiceID == "" {
			continue
		}
		out.Locals = append(out.Locals, proto.LocalEntry{
			ForwardID:        f.ID,
			LocalPort:        f.LocalPort,
			RemotePublicPort: f.RemotePublicPort,
			Protocol:         f.Protocol,
		})
	}
	api.WriteJSON(w, http.StatusOK, out)
}

// POST /api/forwards  (device auth)
func (h *Handler) HandleAddForward(w http.ResponseWriter, r *http.Request) {
	dev, _ := deviceFromContext(r)
	var req proto.AddForwardRequest
	if err := api.DecodeJSON(r, &req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "请求体非法 JSON: "+err.Error())
		return
	}
	if req.RemoteServiceID == "" {
		api.WriteError(w, http.StatusBadRequest, "remote_service_id 必填")
		return
	}
	if req.LocalPort <= 0 || req.LocalPort > 65535 {
		api.WriteError(w, http.StatusBadRequest, "local_port 必须在 1-65535")
		return
	}

	remote, err := h.Store.GetService(r.Context(), req.RemoteServiceID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			api.WriteError(w, http.StatusNotFound, "目标服务不存在")
			return
		}
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	id, err := auth.NewRandomID()
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	f := store.Forward{
		ID:              id,
		OwnerDeviceID:   dev.ID,
		RemoteServiceID: req.RemoteServiceID,
		LocalPort:       req.LocalPort,
		Enabled:         true,
		CreatedAt:       time.Now().UTC(),
	}
	if err := h.Store.CreateForward(r.Context(), f); err != nil {
		if store.IsUniqueViolation(err) {
			api.WriteError(w, http.StatusConflict, "本机已存在相同 local_port 的 forward")
			return
		}
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// remoteOwner 取 remote service 所属 device（不一定就是 dev 自己）
	remoteOwner, err := h.Store.GetDeviceByID(r.Context(), remote.DeviceID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.Auditor.Write(r.Context(), api.DeviceActor(dev.ID), api.ActionForwardAdd, f.ID,
		map[string]any{"remote_service_id": f.RemoteServiceID, "local_port": f.LocalPort})
	api.WriteJSON(w, http.StatusCreated, api.ToForwardDTO(f, remote, remoteOwner))
}

// GET /api/forwards  (device auth)
// 用 ListForwardsJoined 一次拿全：避免对每条 forward 单独 GetService + GetDeviceByID。
func (h *Handler) HandleListForwards(w http.ResponseWriter, r *http.Request) {
	dev, _ := deviceFromContext(r)
	list, err := h.Store.ListForwardsJoined(r.Context(), dev.ID)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := proto.ListForwardsResponse{Forwards: make([]proto.ForwardDTO, 0, len(list))}
	for _, it := range list {
		if it.RemoteServiceID == "" {
			continue // 关联缺失，FK CASCADE 应已清理
		}
		out.Forwards = append(out.Forwards, forwardItemToDTO(it))
	}
	api.WriteJSON(w, http.StatusOK, out)
}

// DELETE /api/forwards/{id}                (device auth, 仅 owner)
// POST   /api/forwards/{id}/enable          (device auth, 仅 owner)
// POST   /api/forwards/{id}/disable         (device auth, 仅 owner)
func (h *Handler) HandleForwardItem(w http.ResponseWriter, r *http.Request) {
	dev, _ := deviceFromContext(r)

	rest := strings.TrimPrefix(r.URL.Path, "/api/forwards/")
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	if id == "" {
		api.WriteError(w, http.StatusBadRequest, "URL 中缺少 forward id")
		return
	}
	var action string
	if len(parts) == 2 {
		action = parts[1]
	}

	f, err := h.Store.GetForward(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			api.WriteError(w, http.StatusNotFound, "forward 不存在")
			return
		}
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if f.OwnerDeviceID != dev.ID {
		api.WriteError(w, http.StatusNotFound, "forward 不存在")
		return
	}

	switch {
	case action == "" && r.Method == http.MethodDelete:
		if err := h.Store.DeleteForward(r.Context(), id); err != nil {
			api.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		h.Auditor.Write(r.Context(), api.DeviceActor(dev.ID), api.ActionForwardDelete, id, nil)
		w.WriteHeader(http.StatusNoContent)
	case action == "enable" && r.Method == http.MethodPost:
		if err := h.Store.SetForwardEnabled(r.Context(), id, true); err != nil {
			api.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		f.Enabled = true
		h.Auditor.Write(r.Context(), api.DeviceActor(dev.ID), api.ActionForwardEnable, id, nil)
		api.WriteForwardDTO(w, r, h.Store, f)
	case action == "disable" && r.Method == http.MethodPost:
		if err := h.Store.SetForwardEnabled(r.Context(), id, false); err != nil {
			api.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		f.Enabled = false
		h.Auditor.Write(r.Context(), api.DeviceActor(dev.ID), api.ActionForwardDisable, id, nil)
		api.WriteForwardDTO(w, r, h.Store, f)
	default:
		api.WriteError(w, http.StatusMethodNotAllowed, "不支持的方法或路径")
	}
}

// GET /api/devices/me/discover  (device auth)
//
// 服务自动发现：返回 mesh 中**其它设备**已启用的全部服务，本机自己的服务被过滤掉
// （forward 到自己没意义且会撞端口）。客户端用 `omlctl service ls --discover` / Tauri
// 的「+ 添加 forward」对话框就读这条结果。
//
// 与 /api/services/all 的差别：
//   - /api/services/all 返回 *所有* 设备的服务（含本机 + disabled），admin / 调试用
//   - /discover 视角是 "当前 device 看到的其它设备"，仅 enabled，是 mesh 用户视角
func (h *Handler) HandleDiscover(w http.ResponseWriter, r *http.Request) {
	// deviceCtxKey 存的是整个 store.Device 而非 ID 字符串——历史教训：早期版本直接
	// 写 `.(string)` 静默失败，myDeviceID="" 永不匹配 it.DeviceID，本机服务也被
	// "discover" 返回回来。两年没人察觉直到 handler 单测覆盖到。
	myDev, _ := deviceFromContext(r)
	myDeviceID := myDev.ID
	list, err := h.Store.ListServicesJoined(r.Context(), "")
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := proto.DiscoverDTO{Services: make([]proto.ServiceBriefDTO, 0, len(list))}
	for _, it := range list {
		if it.DeviceID == myDeviceID {
			continue // 排除本机服务
		}
		if !it.Enabled {
			continue
		}
		out.Services = append(out.Services, proto.ServiceBriefDTO{
			ID:         it.ID,
			DeviceID:   it.DeviceID,
			DeviceName: it.DeviceName,
			Name:       it.Name,
			Protocol:   it.Protocol,
			PublicPort: it.PublicPort,
			Enabled:    it.Enabled,
		})
	}
	api.WriteJSON(w, http.StatusOK, out)
}

// GET /api/services/all  (device auth)
// 跨设备 listing，方便客户端挑要 forward 的服务。
// 用 ListServicesJoined 一次 JOIN 拿全：避免 N+1。
func (h *Handler) HandleListAllServices(w http.ResponseWriter, r *http.Request) {
	list, err := h.Store.ListServicesJoined(r.Context(), "")
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := proto.ListAllServicesResponse{Services: make([]proto.ServiceBriefDTO, 0, len(list))}
	for _, it := range list {
		out.Services = append(out.Services, proto.ServiceBriefDTO{
			ID:         it.ID,
			DeviceID:   it.DeviceID,
			DeviceName: it.DeviceName,
			Name:       it.Name,
			Protocol:   it.Protocol,
			PublicPort: it.PublicPort,
			Enabled:    it.Enabled,
		})
	}
	api.WriteJSON(w, http.StatusOK, out)
}

// forwardItemToDTO 把 store.ForwardListItem 平铺为 proto.ForwardDTO。
func forwardItemToDTO(it store.ForwardListItem) proto.ForwardDTO {
	return proto.ForwardDTO{
		ID:                it.ID,
		OwnerDeviceID:     it.OwnerDeviceID,
		RemoteServiceID:   it.RemoteServiceID,
		RemoteDeviceID:    it.RemoteDeviceID,
		RemoteServiceName: it.RemoteServiceName,
		LocalPort:         it.LocalPort,
		RemotePublicPort:  it.RemotePublicPort,
		Protocol:          it.Protocol,
		Enabled:           it.Enabled,
		CreatedAt:         it.CreatedAt,
	}
}

