// Package admin 实现 /api/admin/* 的所有 handler——devices/services/forwards
// 的 admin CRUD、metrics、audit、enrollment token issue、bark 推送配置、日志查看。
// 内部按职责拆成多个 .go 文件（admin.go / bark.go / logs.go）。
//
// Handler 持有依赖（store/tunnel/ports/enroll/auditor/logBuf/...）；由 server orchestrator
// 在 router.go 构造并 wire 路由。共享 HTTP 工具 / DTO mapper / ctx keys 都在 internal/server/api/。
package admin

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/zhiying8710/oh-my-lan/internal/auth"
	"github.com/zhiying8710/oh-my-lan/internal/enroll"
	"github.com/zhiying8710/oh-my-lan/internal/logging"
	"github.com/zhiying8710/oh-my-lan/internal/proto"
	"github.com/zhiying8710/oh-my-lan/internal/server/api"
	"github.com/zhiying8710/oh-my-lan/internal/server/sshacct"
	"github.com/zhiying8710/oh-my-lan/internal/store"
	"github.com/zhiying8710/oh-my-lan/internal/tunnel"
	"github.com/zhiying8710/oh-my-lan/internal/version"
)

const (
	// AdminCookie 是浏览器登录后写入的 cookie；与 Authorization 头等价。
	AdminCookie = "oml_admin"
)

// Handler 是 admin 子包的入口对象。
//
// 依赖列表比 device/auth 多——因为 admin 端点几乎覆盖整套领域操作：撤销 device 要 tunnel、
// 创建 service 要 ports，logs 端点要 logBuf。RemoveDevice 反向需要 chisel tunnel。
//
// LogBufFn 是 getter 而非直接 *RingBuffer——server 的 logBuf 字段在 New() 后可能被外部
// 注入（测试场景常见），用函数让 mutation 穿透到 handler 调用时刻。
type Handler struct {
	Store               *store.Store
	Tunnel              *tunnel.Server
	Ports               *api.PortAllocator
	Enroll              *enroll.Service
	Auditor             *api.Auditor
	Logger              *slog.Logger
	LogBufFn            func() *logging.RingBuffer
	ChiselAdvertiseAddr string
	StartedAt           time.Time
	// SSH 跳板账号管理。nil = 测试场景。
	SSHAcct *sshacct.Manager
}

// AuthMiddleware: admin 子包对外暴露的 middleware，包了 api.AuthAdminMiddleware。
// router.go 用它包整个 /api/admin/* mux。
func (h *Handler) AuthMiddleware(next http.Handler) http.Handler {
	return api.AuthAdminMiddleware(h.Store, next)
}

// writeForwardDTO 已统一到 api.WriteForwardDTO（自由函数），admin/device 都共用。

// authAdminMiddleware + extractAdminToken 的本地实现已被 api.AuthAdminMiddleware /
// api.ExtractAdminToken 取代。Handler.AuthMiddleware 在文件顶部转发。

// GET /api/admin/info
func (h *Handler) HandleAdminInfo(w http.ResponseWriter, r *http.Request) {
	api.WriteJSON(w, http.StatusOK, proto.AdminInfoResponse{
		ServerFingerprint: h.Tunnel.Fingerprint(),
		ChiselAddr:        h.ChiselAdvertiseAddr,
		PortPoolMin:       h.Ports.Min(),
		PortPoolMax:       h.Ports.Max(),
		Version:           version.Version,
	})
}

// GET /api/admin/devices
// 用 ListDevicesWithCounts 单次 SQL 拿全：消除原来"每设备两次子查询"的 N+1。
func (h *Handler) HandleAdminListDevices(w http.ResponseWriter, r *http.Request) {
	devices, err := h.Store.ListDevicesWithCounts(r.Context())
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := proto.AdminListDevicesResponse{Devices: make([]proto.AdminDeviceDTO, 0, len(devices))}
	for _, d := range devices {
		out.Devices = append(out.Devices, proto.AdminDeviceDTO{
			ID:            d.ID,
			Name:          d.Name,
			Status:        d.Status,
			LastSeenAt:    d.LastSeenAt,
			CreatedAt:     d.CreatedAt,
			ServicesCount: d.ServicesCount,
			ForwardsCount: d.ForwardsCount,
		})
	}
	api.WriteJSON(w, http.StatusOK, out)
}

// GET /api/admin/services
// 用 ListServicesJoined("") 一次 JOIN devices 拿全。
func (h *Handler) HandleAdminListServices(w http.ResponseWriter, r *http.Request) {
	list, err := h.Store.ListServicesJoined(r.Context(), "")
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := proto.AdminListServicesResponse{Services: make([]proto.AdminServiceDTO, 0, len(list))}
	for _, it := range list {
		out.Services = append(out.Services, proto.AdminServiceDTO{
			ID:          it.ID,
			DeviceID:    it.DeviceID,
			DeviceName:  it.DeviceName,
			Name:        it.Name,
			Protocol:    it.Protocol,
			LocalAddr:   it.LocalAddr,
			PublicPort:  it.PublicPort,
			Enabled:     it.Enabled,
			BindLocal:   it.BindLocal,
			CreatedAt:   it.CreatedAt,
			LastProbeAt: it.LastProbeAt,
			LastProbeOK: it.LastProbeOK,
		})
	}
	api.WriteJSON(w, http.StatusOK, out)
}

// POST /api/admin/services  (admin auth)
// body: AdminAddServiceRequest
func (h *Handler) HandleAdminCreateService(w http.ResponseWriter, r *http.Request) {
	var req proto.AdminAddServiceRequest
	if err := api.DecodeJSON(r, &req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "请求体非法 JSON: "+err.Error())
		return
	}
	if req.DeviceID == "" || req.Name == "" || req.LocalAddr == "" {
		api.WriteError(w, http.StatusBadRequest, "device_id / name / local_addr 必填")
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
	if _, err := h.Store.GetDeviceByID(r.Context(), req.DeviceID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			api.WriteError(w, http.StatusNotFound, "device 不存在")
			return
		}
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
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
	// BindLocal 默认 true（安全默认）。admin 显式 false 才会暴露公网（需 UI 二次确认）。
	bindLocal := true
	if req.BindLocal != nil {
		bindLocal = *req.BindLocal
	}
	svc := store.Service{
		ID: id, DeviceID: req.DeviceID, Name: req.Name,
		Protocol: req.Protocol, LocalAddr: req.LocalAddr,
		PublicPort: port, Enabled: true, BindLocal: bindLocal,
		CreatedAt: time.Now().UTC(),
	}
	if err := h.Store.CreateService(r.Context(), svc); err != nil {
		if store.IsUniqueViolation(err) {
			api.WriteError(w, http.StatusConflict, "该设备已有同名服务或端口冲突")
			return
		}
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.Auditor.Write(r.Context(), api.AdminActor(r), api.ActionServiceAdd, svc.ID,
		map[string]any{"on_behalf_of": svc.DeviceID, "name": svc.Name, "public_port": svc.PublicPort, "bind_local": svc.BindLocal})
	api.WriteJSON(w, http.StatusCreated, api.ToServiceDTO(svc))
}

// handleAdminServiceItem 处理 /api/admin/services/{id}[/(enable|disable)]
func (h *Handler) HandleAdminServiceItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/admin/services/")
	// /api/admin/services/all 也走这里——单独透传到 list-all
	if rest == "all" && r.Method == http.MethodGet {
		h.HandleAdminListServices(w, r)
		return
	}
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
	switch {
	case action == "" && r.Method == http.MethodDelete:
		if err := h.Store.DeleteService(r.Context(), id); err != nil {
			api.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		h.Auditor.Write(r.Context(), api.AdminActor(r), api.ActionServiceDelete, id, nil)
		w.WriteHeader(http.StatusNoContent)
	case action == "enable" && r.Method == http.MethodPost:
		if err := h.Store.SetServiceEnabled(r.Context(), id, true); err != nil {
			api.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		svc.Enabled = true
		h.Auditor.Write(r.Context(), api.AdminActor(r), api.ActionServiceEnable, id, nil)
		api.WriteJSON(w, http.StatusOK, api.ToServiceDTO(svc))
	case action == "disable" && r.Method == http.MethodPost:
		if err := h.Store.SetServiceEnabled(r.Context(), id, false); err != nil {
			api.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		svc.Enabled = false
		h.Auditor.Write(r.Context(), api.AdminActor(r), api.ActionServiceDisable, id, nil)
		api.WriteJSON(w, http.StatusOK, api.ToServiceDTO(svc))
	default:
		api.WriteError(w, http.StatusMethodNotAllowed, "不支持的方法或路径")
	}
}

// POST /api/admin/forwards
func (h *Handler) HandleAdminCreateForward(w http.ResponseWriter, r *http.Request) {
	var req proto.AdminAddForwardRequest
	if err := api.DecodeJSON(r, &req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "请求体非法 JSON: "+err.Error())
		return
	}
	if req.OwnerDeviceID == "" || req.RemoteServiceID == "" {
		api.WriteError(w, http.StatusBadRequest, "owner_device_id / remote_service_id 必填")
		return
	}
	if req.LocalPort <= 0 || req.LocalPort > 65535 {
		api.WriteError(w, http.StatusBadRequest, "local_port 必须在 1-65535")
		return
	}
	if _, err := h.Store.GetDeviceByID(r.Context(), req.OwnerDeviceID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			api.WriteError(w, http.StatusNotFound, "owner device 不存在")
			return
		}
		api.WriteError(w, http.StatusInternalServerError, err.Error())
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
		ID: id, OwnerDeviceID: req.OwnerDeviceID,
		RemoteServiceID: req.RemoteServiceID, LocalPort: req.LocalPort,
		Enabled: true, CreatedAt: time.Now().UTC(),
	}
	if err := h.Store.CreateForward(r.Context(), f); err != nil {
		if store.IsUniqueViolation(err) {
			api.WriteError(w, http.StatusConflict, "该设备已有相同 local_port 的 forward")
			return
		}
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	owner, _ := h.Store.GetDeviceByID(r.Context(), remote.DeviceID)
	h.Auditor.Write(r.Context(), api.AdminActor(r), api.ActionForwardAdd, f.ID,
		map[string]any{"owner": f.OwnerDeviceID, "remote_service_id": f.RemoteServiceID, "local_port": f.LocalPort})
	api.WriteJSON(w, http.StatusCreated, api.ToForwardDTO(f, remote, owner))
}

// handleAdminForwardItem 处理 /api/admin/forwards/{id}[/(enable|disable)]
func (h *Handler) HandleAdminForwardItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/admin/forwards/")
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
	switch {
	case action == "" && r.Method == http.MethodDelete:
		if err := h.Store.DeleteForward(r.Context(), id); err != nil {
			api.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		h.Auditor.Write(r.Context(), api.AdminActor(r), api.ActionForwardDelete, id, nil)
		w.WriteHeader(http.StatusNoContent)
	case action == "enable" && r.Method == http.MethodPost:
		if err := h.Store.SetForwardEnabled(r.Context(), id, true); err != nil {
			api.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		f.Enabled = true
		h.Auditor.Write(r.Context(), api.AdminActor(r), api.ActionForwardEnable, id, nil)
		api.WriteForwardDTO(w, r, h.Store, f)
	case action == "disable" && r.Method == http.MethodPost:
		if err := h.Store.SetForwardEnabled(r.Context(), id, false); err != nil {
			api.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		f.Enabled = false
		h.Auditor.Write(r.Context(), api.AdminActor(r), api.ActionForwardDisable, id, nil)
		api.WriteForwardDTO(w, r, h.Store, f)
	default:
		api.WriteError(w, http.StatusMethodNotAllowed, "不支持的方法或路径")
	}
}

// handleAdminDeviceItem 处理 /api/admin/devices/{id}/revoke
//
// 调用 store.DeleteDevice 后同步从 chisel UserIndex 移除——这就消除了
// 之前"omlserver 必须重启才能彻底撤销 device"的已知限制。
func (h *Handler) HandleAdminDeviceItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/admin/devices/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 {
		api.WriteError(w, http.StatusNotFound, "未知 admin device 路径")
		return
	}
	id, action := parts[0], parts[1]
	if action != "revoke" && action != "kick" {
		api.WriteError(w, http.StatusNotFound, "未知 admin device 动作: "+action)
		return
	}
	if r.Method != http.MethodPost {
		api.WriteError(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	if id == "" {
		api.WriteError(w, http.StatusBadRequest, "缺少 device id")
		return
	}
	dev, err := h.Store.GetDeviceByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			api.WriteError(w, http.StatusNotFound, "device 不存在")
			return
		}
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// kick：软重置——锁出 chisel UserIndex 1s 后再加回，让 daemon 重连。
	// 注意：chisel 在 handshake 时检查 user，已建立 session 中途不再校验，所以"踢出"
	// 并不能强行断 TCP。但 daemon 端 keep-alive 失败窗口（10s × 3 = 30s）内任何
	// 真的卡死的 stale session 都会自然 RST 并 reconnect，那时 user index 已恢复。
	// 适用场景：mac 断网恢复后 mac-side 仍能连但 windows-side R-listener mux 卡死，
	// 这一秒锁让 windows 侧重新握手新 sub-stream。
	if action == "kick" {
		h.Tunnel.RemoveDevice(id)
		time.Sleep(time.Second)
		if err := h.Tunnel.AddDevice(dev.ID, dev.TunnelSecret); err != nil {
			h.Logger.Warn("kick 后重新注入 chisel user 失败", "device", id, "err", err)
			api.WriteError(w, http.StatusInternalServerError, "重新注入 chisel user 失败: "+err.Error())
			return
		}
		h.Auditor.Write(r.Context(), api.AdminActor(r), api.ActionDeviceKick, id, nil)
		h.Logger.Info("admin kick device session", "device", id, "name", dev.Name)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// 下面是 revoke 路径——原逻辑不变
	// 撤销三步走：
	//   1) 删 device row（DB cascade 清 services + forwards）
	//   2) chisel UserIndex 移除（运行中 session 立即失效）
	//   3) SSH 账号 lock（authorized_keys 清空 + 强断 ssh -L 跳板）+ 标 ssh_locked_at，
	//      cron 7 天后真 userdel -r。
	// 历史教训：之前只做 1 + 2，攻击者拿到 oml-* 账号的 ssh key 后仍能 forward 任意端口。
	if err := h.Store.MarkDeviceSSHLocked(r.Context(), id, time.Now()); err != nil && !errors.Is(err, store.ErrNotFound) {
		h.Logger.Warn("标记 device 锁定失败（继续 revoke 流程）", "device", id, "err", err)
	}
	if err := h.Store.DeleteDevice(r.Context(), id); err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.Tunnel.RemoveDevice(id)
	if h.SSHAcct != nil {
		if err := h.SSHAcct.Lock(r.Context(), id); err != nil {
			// SSH lock 失败不阻断 revoke——但要警告 + audit 留痕
			h.Logger.Warn("SSH 账号 lock 失败", "device", id, "err", err)
			h.Auditor.Write(r.Context(), api.AdminActor(r), api.ActionDeviceRevoke, id,
				map[string]any{"ssh_lock_err": err.Error()})
		}
	}
	h.Auditor.Write(r.Context(), api.AdminActor(r), api.ActionDeviceRevoke, id, nil)
	h.Logger.Info("admin 撤销 device", "device", id)
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/admin/enroll/tokens   (admin auth)
// 与 /api/enroll/tokens 等价，但允许远程持有 admin token 的客户端生成 enrollment token，
// 不再需要登录到服务器本机执行 `omlserver token create`。
func (h *Handler) HandleAdminIssueToken(w http.ResponseWriter, r *http.Request) {
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
	h.Auditor.Write(r.Context(), api.AdminActor(r), api.ActionEnrollTokenIssue, issued.ID, nil)
	api.WriteJSON(w, http.StatusCreated, proto.IssueTokenResponse{
		ID:        issued.ID,
		Token:     issued.Token,
		ExpiresAt: issued.ExpiresAt,
	})
}

// GET /api/admin/metrics
// 一次 SQL 拿全所有计数；endpoint 本身设计为可被外部抓取做监控（仍需 admin token）。
func (h *Handler) HandleAdminMetrics(w http.ResponseWriter, r *http.Request) {
	counts, err := h.Store.LoadCounts(r.Context())
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	used, _ := h.Store.UsedPublicPorts(r.Context())
	resp := proto.AdminMetricsResponse{
		DevicesTotal:     counts.DevicesTotal,
		DevicesOnline:    counts.DevicesOnline,
		ServicesTotal:    counts.ServicesTotal,
		ServicesEnabled:  counts.ServicesEnabled,
		ForwardsTotal:    counts.ForwardsTotal,
		ForwardsEnabled:  counts.ForwardsEnabled,
		AdminTokensTotal: counts.AdminTokensTotal,
		PortPoolUsed:     len(used),
		PortPoolSize:     h.Ports.Max() - h.Ports.Min() + 1,
		UptimeSeconds:    int64(time.Since(h.StartedAt).Seconds()),
	}
	api.WriteJSON(w, http.StatusOK, resp)
}

// GET /api/admin/audit
// 默认返回最近 200 条，可用 ?limit=N（上限 1000）。
func (h *Handler) HandleAdminListAudit(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		// 不报错：解析失败用默认
		_, _ = fmt.Sscanf(v, "%d", &limit)
	}
	entries, err := h.Store.ListAuditRecent(r.Context(), limit)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := proto.AdminListAuditResponse{Entries: make([]proto.AuditEntryDTO, 0, len(entries))}
	for _, e := range entries {
		out.Entries = append(out.Entries, proto.AuditEntryDTO{
			ID:     e.ID,
			TS:     e.TS,
			Actor:  e.Actor,
			Action: e.Action,
			Target: e.Target,
			Detail: e.Detail,
		})
	}
	api.WriteJSON(w, http.StatusOK, out)
}

// GET /api/admin/forwards
// 用 ListForwardsJoined("") 一次拿全：消除原"devices × forwards × services"三层嵌套 N+1。
func (h *Handler) HandleAdminListForwards(w http.ResponseWriter, r *http.Request) {
	list, err := h.Store.ListForwardsJoined(r.Context(), "")
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := proto.AdminListForwardsResponse{Forwards: make([]proto.AdminForwardDTO, 0, len(list))}
	for _, it := range list {
		if it.RemoteServiceID == "" {
			continue
		}
		out.Forwards = append(out.Forwards, proto.AdminForwardDTO{
			ID:                it.ID,
			OwnerDeviceID:     it.OwnerDeviceID,
			OwnerDeviceName:   it.OwnerDeviceName,
			RemoteServiceID:   it.RemoteServiceID,
			RemoteServiceName: it.RemoteServiceName,
			RemoteDeviceID:    it.RemoteDeviceID,
			RemoteDeviceName:  it.RemoteDeviceName,
			LocalPort:         it.LocalPort,
			RemotePublicPort:  it.RemotePublicPort,
			Protocol:          it.Protocol,
			Enabled:           it.Enabled,
			CreatedAt:         it.CreatedAt,
		})
	}
	api.WriteJSON(w, http.StatusOK, out)
}
