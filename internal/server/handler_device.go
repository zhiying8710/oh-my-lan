package server

// handler_device.go：所有走 device bearer 认证（或本机/公开）的 HTTP handler。
// 共用工具（writeJSON / writeError / decodeJSON / DTO mapper）都在 helpers.go。

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/zhiying8710/oh-my-lan/internal/auth"
	"github.com/zhiying8710/oh-my-lan/internal/enroll"
	"github.com/zhiying8710/oh-my-lan/internal/proto"
	"github.com/zhiying8710/oh-my-lan/internal/store"
)

// POST /api/enroll/tokens  (local only)
func (s *Server) handleIssueToken(w http.ResponseWriter, r *http.Request) {
	var req proto.IssueTokenRequest
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "请求体非法 JSON: "+err.Error())
			return
		}
	}
	ttl := time.Duration(req.TTLSeconds) * time.Second
	issued, err := s.enroll.IssueToken(r.Context(), ttl)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, proto.IssueTokenResponse{
		ID:        issued.ID,
		Token:     issued.Token,
		ExpiresAt: issued.ExpiresAt,
	})
}

// POST /api/devices/enroll
func (s *Server) handleEnrollDevice(w http.ResponseWriter, r *http.Request) {
	var req proto.EnrollDeviceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体非法 JSON: "+err.Error())
		return
	}
	dev, err := s.enroll.EnrollDevice(r.Context(), req.Token, strings.TrimSpace(req.DeviceName))
	switch {
	case errors.Is(err, enroll.ErrTokenInvalid), errors.Is(err, enroll.ErrTokenExpired):
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	case errors.Is(err, enroll.ErrDeviceExists):
		writeError(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// 注册成功，把设备注入 chisel server 的 user index，让它能立刻拨号上来。
	if err := s.tunnel.AddDevice(dev.DeviceID, dev.TunnelSecret); err != nil {
		writeError(w, http.StatusInternalServerError, "chisel 用户注册失败: "+err.Error())
		return
	}

	s.audit(r.Context(), "system", ActionDeviceEnroll, dev.DeviceID,
		map[string]any{"device_name": dev.DeviceName})

	writeJSON(w, http.StatusCreated, proto.EnrollDeviceResponse{
		DeviceID:          dev.DeviceID,
		DeviceName:        dev.DeviceName,
		TunnelSecret:      dev.TunnelSecret,
		ServerFingerprint: s.tunnel.Fingerprint(),
		ChiselAddr:        s.chiselAdvertiseAddr,
	})
}

// POST /api/services  (device auth)
func (s *Server) handleAddService(w http.ResponseWriter, r *http.Request) {
	dev, _ := deviceFromContext(r.Context())
	var req proto.AddServiceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体非法 JSON: "+err.Error())
		return
	}
	if req.Name == "" || req.LocalAddr == "" {
		writeError(w, http.StatusBadRequest, "name / local_addr 必填")
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
		ID:         id,
		DeviceID:   dev.ID,
		Name:       req.Name,
		Protocol:   req.Protocol,
		LocalAddr:  req.LocalAddr,
		PublicPort: port,
		Enabled:    true,
		CreatedAt:  time.Now().UTC(),
	}
	if err := s.store.CreateService(r.Context(), svc); err != nil {
		if store.IsUniqueViolation(err) {
			writeError(w, http.StatusConflict, "服务名已存在或端口冲突")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r.Context(), deviceActor(dev.ID), ActionServiceAdd, svc.ID,
		map[string]any{"name": svc.Name, "protocol": svc.Protocol, "public_port": svc.PublicPort})
	writeJSON(w, http.StatusCreated, toServiceDTO(svc))
}

// GET /api/services  (device auth)
func (s *Server) handleListServices(w http.ResponseWriter, r *http.Request) {
	dev, _ := deviceFromContext(r.Context())
	list, err := s.store.ListServicesByDevice(r.Context(), dev.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := proto.ListServicesResponse{Services: make([]proto.ServiceDTO, 0, len(list))}
	for _, sv := range list {
		out.Services = append(out.Services, toServiceDTO(sv))
	}
	writeJSON(w, http.StatusOK, out)
}

// DELETE /api/services/{id}                (device auth)
// POST   /api/services/{id}/enable          (device auth)
// POST   /api/services/{id}/disable         (device auth)
func (s *Server) handleServiceItem(w http.ResponseWriter, r *http.Request) {
	dev, _ := deviceFromContext(r.Context())

	rest := strings.TrimPrefix(r.URL.Path, "/api/services/")
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
	if svc.DeviceID != dev.ID {
		// 跨设备访问返回 404 以避免泄漏 id 存在性
		writeError(w, http.StatusNotFound, "服务不存在")
		return
	}

	switch {
	case action == "" && r.Method == http.MethodDelete:
		if err := s.store.DeleteService(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.audit(r.Context(), deviceActor(dev.ID), ActionServiceDelete, id, nil)
		w.WriteHeader(http.StatusNoContent)
	case action == "enable" && r.Method == http.MethodPost:
		if err := s.store.SetServiceEnabled(r.Context(), id, true); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		svc.Enabled = true
		s.audit(r.Context(), deviceActor(dev.ID), ActionServiceEnable, id, nil)
		writeJSON(w, http.StatusOK, toServiceDTO(svc))
	case action == "disable" && r.Method == http.MethodPost:
		if err := s.store.SetServiceEnabled(r.Context(), id, false); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		svc.Enabled = false
		s.audit(r.Context(), deviceActor(dev.ID), ActionServiceDisable, id, nil)
		writeJSON(w, http.StatusOK, toServiceDTO(svc))
	default:
		writeError(w, http.StatusMethodNotAllowed, "不支持的方法或路径")
	}
}

// GET /api/devices/me/bootstrap  (device auth)
//
// 同时充当设备 keepalive：每次调用更新 last_seen_at + status=online。
// 客户端 daemon 默认每 30s 调一次（M2.1 reload watcher）。
// 内部用 JOIN 查询避免 N+1：services + forwards 各一次 SQL。
func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	dev, _ := deviceFromContext(r.Context())
	if err := s.store.UpdateDeviceStatus(r.Context(), dev.ID, store.DeviceStatusOnline, time.Now()); err != nil {
		// keepalive 失败不阻断业务，记日志继续
		s.logger.Warn("更新设备 last_seen 失败", "device", dev.ID, "err", err)
	}

	myServices, err := s.store.ListServicesByDevice(r.Context(), dev.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	myForwards, err := s.store.ListForwardsJoined(r.Context(), dev.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := proto.BootstrapResponse{
		ServerFingerprint: s.tunnel.Fingerprint(),
		ChiselAddr:        s.chiselAdvertiseAddr,
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
	writeJSON(w, http.StatusOK, out)
}

// POST /api/forwards  (device auth)
func (s *Server) handleAddForward(w http.ResponseWriter, r *http.Request) {
	dev, _ := deviceFromContext(r.Context())
	var req proto.AddForwardRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体非法 JSON: "+err.Error())
		return
	}
	if req.RemoteServiceID == "" {
		writeError(w, http.StatusBadRequest, "remote_service_id 必填")
		return
	}
	if req.LocalPort <= 0 || req.LocalPort > 65535 {
		writeError(w, http.StatusBadRequest, "local_port 必须在 1-65535")
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
		ID:              id,
		OwnerDeviceID:   dev.ID,
		RemoteServiceID: req.RemoteServiceID,
		LocalPort:       req.LocalPort,
		Enabled:         true,
		CreatedAt:       time.Now().UTC(),
	}
	if err := s.store.CreateForward(r.Context(), f); err != nil {
		if store.IsUniqueViolation(err) {
			writeError(w, http.StatusConflict, "本机已存在相同 local_port 的 forward")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// remoteOwner 取 remote service 所属 device（不一定就是 dev 自己）
	remoteOwner, err := s.store.GetDeviceByID(r.Context(), remote.DeviceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r.Context(), deviceActor(dev.ID), ActionForwardAdd, f.ID,
		map[string]any{"remote_service_id": f.RemoteServiceID, "local_port": f.LocalPort})
	writeJSON(w, http.StatusCreated, toForwardDTO(f, remote, remoteOwner))
}

// GET /api/forwards  (device auth)
// 用 ListForwardsJoined 一次拿全：避免对每条 forward 单独 GetService + GetDeviceByID。
func (s *Server) handleListForwards(w http.ResponseWriter, r *http.Request) {
	dev, _ := deviceFromContext(r.Context())
	list, err := s.store.ListForwardsJoined(r.Context(), dev.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := proto.ListForwardsResponse{Forwards: make([]proto.ForwardDTO, 0, len(list))}
	for _, it := range list {
		if it.RemoteServiceID == "" {
			continue // 关联缺失，FK CASCADE 应已清理
		}
		out.Forwards = append(out.Forwards, forwardItemToDTO(it))
	}
	writeJSON(w, http.StatusOK, out)
}

// DELETE /api/forwards/{id}                (device auth, 仅 owner)
// POST   /api/forwards/{id}/enable          (device auth, 仅 owner)
// POST   /api/forwards/{id}/disable         (device auth, 仅 owner)
func (s *Server) handleForwardItem(w http.ResponseWriter, r *http.Request) {
	dev, _ := deviceFromContext(r.Context())

	rest := strings.TrimPrefix(r.URL.Path, "/api/forwards/")
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
	if f.OwnerDeviceID != dev.ID {
		writeError(w, http.StatusNotFound, "forward 不存在")
		return
	}

	switch {
	case action == "" && r.Method == http.MethodDelete:
		if err := s.store.DeleteForward(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.audit(r.Context(), deviceActor(dev.ID), ActionForwardDelete, id, nil)
		w.WriteHeader(http.StatusNoContent)
	case action == "enable" && r.Method == http.MethodPost:
		if err := s.store.SetForwardEnabled(r.Context(), id, true); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		f.Enabled = true
		s.audit(r.Context(), deviceActor(dev.ID), ActionForwardEnable, id, nil)
		writeForwardDTO(w, r, s, f)
	case action == "disable" && r.Method == http.MethodPost:
		if err := s.store.SetForwardEnabled(r.Context(), id, false); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		f.Enabled = false
		s.audit(r.Context(), deviceActor(dev.ID), ActionForwardDisable, id, nil)
		writeForwardDTO(w, r, s, f)
	default:
		writeError(w, http.StatusMethodNotAllowed, "不支持的方法或路径")
	}
}

// writeForwardDTO 把 forward 解引用为 ForwardDTO 后写出（用于 enable/disable 后返回最新状态）。
// 仅 2 次 query（GetService + GetDevice），不在循环中调用，不构成 N+1。
func writeForwardDTO(w http.ResponseWriter, r *http.Request, s *Server, f store.Forward) {
	remote, err := s.store.GetService(r.Context(), f.RemoteServiceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	owner, err := s.store.GetDeviceByID(r.Context(), remote.DeviceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toForwardDTO(f, remote, owner))
}

// GET /api/services/all  (device auth)
// 跨设备 listing，方便客户端挑要 forward 的服务。
// 用 ListServicesJoined 一次 JOIN 拿全：避免 N+1。
func (s *Server) handleListAllServices(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListServicesJoined(r.Context(), "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
	writeJSON(w, http.StatusOK, out)
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

