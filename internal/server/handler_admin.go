package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zhiying8710/oh-my-lan/internal/auth"
	"github.com/zhiying8710/oh-my-lan/internal/enroll"
	"github.com/zhiying8710/oh-my-lan/internal/proto"
	"github.com/zhiying8710/oh-my-lan/internal/store"
	"github.com/zhiying8710/oh-my-lan/internal/version"
)

const (
	// AdminCookie 是浏览器登录后写入的 cookie；与 Authorization 头等价。
	AdminCookie = "oml_admin"
)

// authAdminMiddleware 校验"管理员级"凭证。
// 接受三种凭证（按优先级）：
//  1. 账号密码登录后的 session token（Bearer 头或 oml_admin cookie，前缀 sess_）
//  2. CLI 创建的长期 admin token（Bearer 头或 oml_admin cookie）
//  3. 都没有 → 401
//
// 校验通过后 actor 写入 context（audit 用），并按对应表 touch last-used。
//
// 设计意图：UI 默认走密码登录拿 session；admin token 保留给机器对机器
// （CI、监控 metrics 抓取、curl 脚本），两条路径行为一致。
func (s *Server) authAdminMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := extractAdminToken(r)
		if raw == "" {
			writeError(w, http.StatusUnauthorized, "未登录或缺少凭证")
			return
		}
		hash := auth.HashSecret(raw)
		now := time.Now()

		// ① 先试 session
		if sess, err := s.store.GetActiveSessionByHash(r.Context(), hash, now); err == nil {
			u, uerr := s.store.GetAdminUserByID(r.Context(), sess.UserID)
			if uerr == nil {
				if err := s.store.TouchSession(r.Context(), sess.ID, now); err != nil {
					s.logger.Warn("touch session 失败", "err", err)
				}
				ctx := withActor(r.Context(), "user:"+u.Username, u.ID)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			// 用户被删 → fallthrough 当 admin_token 试
		}

		// ② 再试 admin_token
		tok, err := s.store.GetAdminTokenByHash(r.Context(), hash)
		if err == nil {
			if err := s.store.TouchAdminToken(r.Context(), tok.ID, now); err != nil {
				s.logger.Warn("touch admin_token last_used 失败", "err", err)
			}
			// 用 token hash 前 16 字符当 actor 简称
			short := hash
			if len(short) > 16 {
				short = short[:16]
			}
			ctx := withActor(r.Context(), "admin:"+short, "")
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		writeError(w, http.StatusUnauthorized, "凭证无效或已失效")
	})
}

func extractAdminToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, bearerPrefix) {
		return strings.TrimPrefix(h, bearerPrefix)
	}
	if c, err := r.Cookie(AdminCookie); err == nil {
		return c.Value
	}
	return ""
}

// GET /api/admin/info
func (s *Server) handleAdminInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, proto.AdminInfoResponse{
		ServerFingerprint: s.tunnel.Fingerprint(),
		ChiselAddr:        s.chiselAdvertiseAddr,
		PortPoolMin:       s.ports.min,
		PortPoolMax:       s.ports.max,
		Version:           version.Version,
	})
}

// GET /api/admin/devices
// 用 ListDevicesWithCounts 单次 SQL 拿全：消除原来"每设备两次子查询"的 N+1。
func (s *Server) handleAdminListDevices(w http.ResponseWriter, r *http.Request) {
	devices, err := s.store.ListDevicesWithCounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
	writeJSON(w, http.StatusOK, out)
}

// GET /api/admin/services
// 用 ListServicesJoined("") 一次 JOIN devices 拿全。
func (s *Server) handleAdminListServices(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListServicesJoined(r.Context(), "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := proto.AdminListServicesResponse{Services: make([]proto.AdminServiceDTO, 0, len(list))}
	for _, it := range list {
		out.Services = append(out.Services, proto.AdminServiceDTO{
			ID:         it.ID,
			DeviceID:   it.DeviceID,
			DeviceName: it.DeviceName,
			Name:       it.Name,
			Protocol:   it.Protocol,
			LocalAddr:  it.LocalAddr,
			PublicPort: it.PublicPort,
			Enabled:    it.Enabled,
			CreatedAt:  it.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// POST /api/admin/services  (admin auth)
// body: AdminAddServiceRequest
func (s *Server) handleAdminCreateService(w http.ResponseWriter, r *http.Request) {
	var req proto.AdminAddServiceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体非法 JSON: "+err.Error())
		return
	}
	if req.DeviceID == "" || req.Name == "" || req.LocalAddr == "" {
		writeError(w, http.StatusBadRequest, "device_id / name / local_addr 必填")
		return
	}
	if req.Protocol != store.ProtocolTCP && req.Protocol != store.ProtocolUDP {
		writeError(w, http.StatusBadRequest, "protocol 必须是 tcp 或 udp")
		return
	}
	localAddr, err := normalizeLocalAddr(req.LocalAddr)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.LocalAddr = localAddr
	if _, err := s.store.GetDeviceByID(r.Context(), req.DeviceID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "device 不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	port, err := s.ports.Allocate(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	id, err := auth.NewRandomID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	svc := store.Service{
		ID: id, DeviceID: req.DeviceID, Name: req.Name,
		Protocol: req.Protocol, LocalAddr: req.LocalAddr,
		PublicPort: port, Enabled: true, CreatedAt: time.Now().UTC(),
	}
	if err := s.store.CreateService(r.Context(), svc); err != nil {
		if store.IsUniqueViolation(err) {
			writeError(w, http.StatusConflict, "该设备已有同名服务或端口冲突")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r.Context(), adminActor(r), ActionServiceAdd, svc.ID,
		map[string]any{"on_behalf_of": svc.DeviceID, "name": svc.Name, "public_port": svc.PublicPort})
	writeJSON(w, http.StatusCreated, toServiceDTO(svc))
}

// handleAdminServiceItem 处理 /api/admin/services/{id}[/(enable|disable)]
func (s *Server) handleAdminServiceItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/admin/services/")
	// /api/admin/services/all 也走这里——单独透传到 list-all
	if rest == "all" && r.Method == http.MethodGet {
		s.handleAdminListServices(w, r)
		return
	}
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	if id == "" {
		writeError(w, http.StatusBadRequest, "URL 中缺少 service id")
		return
	}
	var action string
	if len(parts) == 2 {
		action = parts[1]
	}
	svc, err := s.store.GetService(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "服务不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	switch {
	case action == "" && r.Method == http.MethodDelete:
		if err := s.store.DeleteService(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.audit(r.Context(), adminActor(r), ActionServiceDelete, id, nil)
		w.WriteHeader(http.StatusNoContent)
	case action == "enable" && r.Method == http.MethodPost:
		if err := s.store.SetServiceEnabled(r.Context(), id, true); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		svc.Enabled = true
		s.audit(r.Context(), adminActor(r), ActionServiceEnable, id, nil)
		writeJSON(w, http.StatusOK, toServiceDTO(svc))
	case action == "disable" && r.Method == http.MethodPost:
		if err := s.store.SetServiceEnabled(r.Context(), id, false); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		svc.Enabled = false
		s.audit(r.Context(), adminActor(r), ActionServiceDisable, id, nil)
		writeJSON(w, http.StatusOK, toServiceDTO(svc))
	default:
		writeError(w, http.StatusMethodNotAllowed, "不支持的方法或路径")
	}
}

// POST /api/admin/forwards
func (s *Server) handleAdminCreateForward(w http.ResponseWriter, r *http.Request) {
	var req proto.AdminAddForwardRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体非法 JSON: "+err.Error())
		return
	}
	if req.OwnerDeviceID == "" || req.RemoteServiceID == "" {
		writeError(w, http.StatusBadRequest, "owner_device_id / remote_service_id 必填")
		return
	}
	if req.LocalPort <= 0 || req.LocalPort > 65535 {
		writeError(w, http.StatusBadRequest, "local_port 必须在 1-65535")
		return
	}
	if _, err := s.store.GetDeviceByID(r.Context(), req.OwnerDeviceID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "owner device 不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	remote, err := s.store.GetService(r.Context(), req.RemoteServiceID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "目标服务不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	id, err := auth.NewRandomID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	f := store.Forward{
		ID: id, OwnerDeviceID: req.OwnerDeviceID,
		RemoteServiceID: req.RemoteServiceID, LocalPort: req.LocalPort,
		Enabled: true, CreatedAt: time.Now().UTC(),
	}
	if err := s.store.CreateForward(r.Context(), f); err != nil {
		if store.IsUniqueViolation(err) {
			writeError(w, http.StatusConflict, "该设备已有相同 local_port 的 forward")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	owner, _ := s.store.GetDeviceByID(r.Context(), remote.DeviceID)
	s.audit(r.Context(), adminActor(r), ActionForwardAdd, f.ID,
		map[string]any{"owner": f.OwnerDeviceID, "remote_service_id": f.RemoteServiceID, "local_port": f.LocalPort})
	writeJSON(w, http.StatusCreated, toForwardDTO(f, remote, owner))
}

// handleAdminForwardItem 处理 /api/admin/forwards/{id}[/(enable|disable)]
func (s *Server) handleAdminForwardItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/admin/forwards/")
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	if id == "" {
		writeError(w, http.StatusBadRequest, "URL 中缺少 forward id")
		return
	}
	var action string
	if len(parts) == 2 {
		action = parts[1]
	}
	f, err := s.store.GetForward(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "forward 不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	switch {
	case action == "" && r.Method == http.MethodDelete:
		if err := s.store.DeleteForward(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.audit(r.Context(), adminActor(r), ActionForwardDelete, id, nil)
		w.WriteHeader(http.StatusNoContent)
	case action == "enable" && r.Method == http.MethodPost:
		if err := s.store.SetForwardEnabled(r.Context(), id, true); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		f.Enabled = true
		s.audit(r.Context(), adminActor(r), ActionForwardEnable, id, nil)
		writeForwardDTO(w, r, s, f)
	case action == "disable" && r.Method == http.MethodPost:
		if err := s.store.SetForwardEnabled(r.Context(), id, false); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		f.Enabled = false
		s.audit(r.Context(), adminActor(r), ActionForwardDisable, id, nil)
		writeForwardDTO(w, r, s, f)
	default:
		writeError(w, http.StatusMethodNotAllowed, "不支持的方法或路径")
	}
}

// handleAdminDeviceItem 处理 /api/admin/devices/{id}/revoke
//
// 调用 store.DeleteDevice 后同步从 chisel UserIndex 移除——这就消除了
// 之前"omlserver 必须重启才能彻底撤销 device"的已知限制。
func (s *Server) handleAdminDeviceItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/admin/devices/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[1] != "revoke" {
		writeError(w, http.StatusNotFound, "未知 admin device 路径")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	id := parts[0]
	if id == "" {
		writeError(w, http.StatusBadRequest, "缺少 device id")
		return
	}
	if _, err := s.store.GetDeviceByID(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "device 不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.store.DeleteDevice(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 运行中同步撤销 chisel session 认证；新连接立即被拒，已建立的 session 在下一次心跳/拨号失败时断开。
	s.tunnel.RemoveDevice(id)
	s.audit(r.Context(), adminActor(r), ActionDeviceRevoke, id, nil)
	s.logger.Info("admin 撤销 device", "device", id)
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/admin/enroll/tokens   (admin auth)
// 与 /api/enroll/tokens 等价，但允许远程持有 admin token 的客户端生成 enrollment token，
// 不再需要登录到服务器本机执行 `omlserver token create`。
func (s *Server) handleAdminIssueToken(w http.ResponseWriter, r *http.Request) {
	var req proto.IssueTokenRequest
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "请求体非法 JSON: "+err.Error())
			return
		}
	}
	ttl := time.Duration(req.TTLSeconds) * time.Second
	issued, err := enroll.New(s.store).IssueToken(r.Context(), ttl)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r.Context(), adminActor(r), ActionEnrollTokenIssue, issued.ID, nil)
	writeJSON(w, http.StatusCreated, proto.IssueTokenResponse{
		ID:        issued.ID,
		Token:     issued.Token,
		ExpiresAt: issued.ExpiresAt,
	})
}

// GET /api/admin/metrics
// 一次 SQL 拿全所有计数；endpoint 本身设计为可被外部抓取做监控（仍需 admin token）。
func (s *Server) handleAdminMetrics(w http.ResponseWriter, r *http.Request) {
	counts, err := s.store.LoadCounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	used, _ := s.store.UsedPublicPorts(r.Context())
	resp := proto.AdminMetricsResponse{
		DevicesTotal:     counts.DevicesTotal,
		DevicesOnline:    counts.DevicesOnline,
		ServicesTotal:    counts.ServicesTotal,
		ServicesEnabled:  counts.ServicesEnabled,
		ForwardsTotal:    counts.ForwardsTotal,
		ForwardsEnabled:  counts.ForwardsEnabled,
		AdminTokensTotal: counts.AdminTokensTotal,
		PortPoolUsed:     len(used),
		PortPoolSize:     s.ports.max - s.ports.min + 1,
		UptimeSeconds:    int64(time.Since(s.startedAt).Seconds()),
	}
	writeJSON(w, http.StatusOK, resp)
}

// GET /api/admin/audit
// 默认返回最近 200 条，可用 ?limit=N（上限 1000）。
func (s *Server) handleAdminListAudit(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		// 不报错：解析失败用默认
		_, _ = fmt.Sscanf(v, "%d", &limit)
	}
	entries, err := s.store.ListAuditRecent(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
	writeJSON(w, http.StatusOK, out)
}

// GET /api/admin/forwards
// 用 ListForwardsJoined("") 一次拿全：消除原"devices × forwards × services"三层嵌套 N+1。
func (s *Server) handleAdminListForwards(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListForwardsJoined(r.Context(), "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
	writeJSON(w, http.StatusOK, out)
}
