package server

import (
	"net/http"

	"github.com/zhiying8710/oh-my-lan/internal/logging"
	"github.com/zhiying8710/oh-my-lan/internal/server/admin"
	"github.com/zhiying8710/oh-my-lan/internal/server/api"
	"github.com/zhiying8710/oh-my-lan/internal/server/auth"
	"github.com/zhiying8710/oh-my-lan/internal/server/device"
)

// registerRoutes 把所有 HTTP 路由挂到 mux 上。
// 分三组：
//   - 公开 / 本机端点：/healthz、/api/enroll/tokens（本机限定）、/api/devices/enroll
//   - 设备认证端点（Bearer device_id.secret）：/api/services*、/api/forwards*、/api/devices/me/bootstrap
//   - 管理员认证端点（Bearer admin token）：/api/admin/*
//   - 静态 Web Admin UI：/admin/、/admin
func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// auditor lazy-init：New() 路径已构造；newTestServer 直接 new(Server) 时为 nil。
	if s.auditor == nil {
		s.auditor = &api.Auditor{Store: s.store, Logger: s.logger}
	}

	// /api/auth/* 由 internal/server/auth/ 子包负责
	authH := &auth.Handler{Store: s.store, Auditor: s.auditor, LoginRL: s.loginRL}
	authH.Register(mux, s.authAdminMiddleware)

	// /api/enroll/tokens、/api/devices/enroll、/api/services*、/api/forwards*、
	// /api/devices/me/* 等设备视角端点由 internal/server/device/ 子包负责。
	devH := &device.Handler{
		Store: s.store, Tunnel: s.tunnel, Ports: s.ports, Enroll: s.enroll,
		Auditor: s.auditor, Logger: s.logger, ChiselAdvertiseAddr: s.chiselAdvertiseAddr,
	}

	// 本机端点：生成 enrollment token
	mux.HandleFunc("/api/enroll/tokens", requireLocal(api.MethodOnly(http.MethodPost, devH.HandleIssueToken)))

	// 公开端点：客户端用 token 注册
	mux.HandleFunc("/api/devices/enroll", api.MethodOnly(http.MethodPost, devH.HandleEnrollDevice))

	// 设备认证端点（device bearer）
	devMux := http.NewServeMux()
	devMux.HandleFunc("/api/services", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			devH.HandleListServices(w, r)
		case http.MethodPost:
			devH.HandleAddService(w, r)
		default:
			api.WriteError(w, http.StatusMethodNotAllowed, "仅支持 GET/POST")
		}
	})
	devMux.HandleFunc("/api/services/all", api.MethodOnly(http.MethodGet, devH.HandleListAllServices))
	devMux.HandleFunc("/api/services/", devH.HandleServiceItem)
	devMux.HandleFunc("/api/forwards", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			devH.HandleListForwards(w, r)
		case http.MethodPost:
			devH.HandleAddForward(w, r)
		default:
			api.WriteError(w, http.StatusMethodNotAllowed, "仅支持 GET/POST")
		}
	})
	devMux.HandleFunc("/api/forwards/", devH.HandleForwardItem)
	devMux.HandleFunc("/api/devices/me/bootstrap", api.MethodOnly(http.MethodGet, devH.HandleBootstrap))
	devMux.HandleFunc("/api/devices/me/discover", api.MethodOnly(http.MethodGet, devH.HandleDiscover))
	mux.Handle("/api/services", s.authDeviceMiddleware(devMux))
	mux.Handle("/api/services/", s.authDeviceMiddleware(devMux))
	mux.Handle("/api/forwards", s.authDeviceMiddleware(devMux))
	mux.Handle("/api/forwards/", s.authDeviceMiddleware(devMux))
	mux.Handle("/api/devices/me/bootstrap", s.authDeviceMiddleware(devMux))
	mux.Handle("/api/devices/me/discover", s.authDeviceMiddleware(devMux))

	// /api/admin/* 由 internal/server/admin/ 子包负责。
	adminH := &admin.Handler{
		Store: s.store, Tunnel: s.tunnel, Ports: s.ports, Enroll: s.enroll,
		Auditor: s.auditor, Logger: s.logger,
		LogBufFn:            func() *logging.RingBuffer { return s.logBuf },
		ChiselAdvertiseAddr: s.chiselAdvertiseAddr, StartedAt: s.startedAt,
	}
	adminMux := http.NewServeMux()
	adminMux.HandleFunc("/api/admin/info", api.MethodOnly(http.MethodGet, adminH.HandleAdminInfo))
	adminMux.HandleFunc("/api/admin/metrics", api.MethodOnly(http.MethodGet, adminH.HandleAdminMetrics))
	adminMux.HandleFunc("/api/admin/audit", api.MethodOnly(http.MethodGet, adminH.HandleAdminListAudit))
	adminMux.HandleFunc("/api/admin/logs", api.MethodOnly(http.MethodGet, adminH.HandleAdminLogs))
	adminMux.HandleFunc("/api/admin/devices", api.MethodOnly(http.MethodGet, adminH.HandleAdminListDevices))
	adminMux.HandleFunc("/api/admin/devices/", adminH.HandleAdminDeviceItem)
	adminMux.HandleFunc("/api/admin/services", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			adminH.HandleAdminListServices(w, r)
		case http.MethodPost:
			adminH.HandleAdminCreateService(w, r)
		default:
			api.WriteError(w, http.StatusMethodNotAllowed, "仅支持 GET/POST")
		}
	})
	adminMux.HandleFunc("/api/admin/services/", adminH.HandleAdminServiceItem)
	adminMux.HandleFunc("/api/admin/forwards", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			adminH.HandleAdminListForwards(w, r)
		case http.MethodPost:
			adminH.HandleAdminCreateForward(w, r)
		default:
			api.WriteError(w, http.StatusMethodNotAllowed, "仅支持 GET/POST")
		}
	})
	adminMux.HandleFunc("/api/admin/forwards/", adminH.HandleAdminForwardItem)
	adminMux.HandleFunc("/api/admin/enroll/tokens", api.MethodOnly(http.MethodPost, adminH.HandleAdminIssueToken))
	adminMux.HandleFunc("/api/admin/bark", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			adminH.HandleAdminBarkGet(w, r)
		case http.MethodPut:
			adminH.HandleAdminBarkPut(w, r)
		default:
			api.WriteError(w, http.StatusMethodNotAllowed, "仅支持 GET/PUT")
		}
	})
	adminMux.HandleFunc("/api/admin/bark/test", api.MethodOnly(http.MethodPost, adminH.HandleAdminBarkTest))
	mux.Handle("/api/admin/", adminH.AuthMiddleware(adminMux))

	// /admin/ 提供嵌入的静态 Web UI；以及 /admin → /admin/ 重定向
	mux.Handle("/admin/", adminWebHandler())
	mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusMovedPermanently)
	})
}
