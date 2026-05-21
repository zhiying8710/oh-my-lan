package server

import "net/http"

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

	// 本机端点：生成 enrollment token
	mux.HandleFunc("/api/enroll/tokens", requireLocal(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "仅支持 POST")
			return
		}
		s.handleIssueToken(w, r)
	}))

	// 公开端点：客户端用 token 注册
	mux.HandleFunc("/api/devices/enroll", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "仅支持 POST")
			return
		}
		s.handleEnrollDevice(w, r)
	})

	// 公开端点：账号密码登录
	mux.HandleFunc("/api/auth/login", methodOnly(http.MethodPost, s.handleAuthLogin))
	// 登出 / me 需要先经过 admin 中间件（拿到 actor 上下文）
	authedAuthMux := http.NewServeMux()
	authedAuthMux.HandleFunc("/api/auth/logout", methodOnly(http.MethodPost, s.handleAuthLogout))
	authedAuthMux.HandleFunc("/api/auth/me", methodOnly(http.MethodGet, s.handleAuthMe))
	mux.Handle("/api/auth/logout", s.authAdminMiddleware(authedAuthMux))
	mux.Handle("/api/auth/me", s.authAdminMiddleware(authedAuthMux))

	// 设备认证端点
	devMux := http.NewServeMux()
	devMux.HandleFunc("/api/services", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.handleListServices(w, r)
		case http.MethodPost:
			s.handleAddService(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "仅支持 GET/POST")
		}
	})
	devMux.HandleFunc("/api/services/all", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "仅支持 GET")
			return
		}
		s.handleListAllServices(w, r)
	})
	devMux.HandleFunc("/api/services/", s.handleServiceItem)
	devMux.HandleFunc("/api/forwards", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.handleListForwards(w, r)
		case http.MethodPost:
			s.handleAddForward(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "仅支持 GET/POST")
		}
	})
	devMux.HandleFunc("/api/forwards/", s.handleForwardItem)
	devMux.HandleFunc("/api/devices/me/bootstrap", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "仅支持 GET")
			return
		}
		s.handleBootstrap(w, r)
	})
	mux.Handle("/api/services", s.authDeviceMiddleware(devMux))
	mux.Handle("/api/services/", s.authDeviceMiddleware(devMux))
	mux.Handle("/api/forwards", s.authDeviceMiddleware(devMux))
	mux.Handle("/api/forwards/", s.authDeviceMiddleware(devMux))
	mux.Handle("/api/devices/me/bootstrap", s.authDeviceMiddleware(devMux))

	// 管理端点（admin token 认证）
	adminMux := http.NewServeMux()
	adminMux.HandleFunc("/api/admin/info", methodOnly(http.MethodGet, s.handleAdminInfo))
	adminMux.HandleFunc("/api/admin/metrics", methodOnly(http.MethodGet, s.handleAdminMetrics))
	adminMux.HandleFunc("/api/admin/audit", methodOnly(http.MethodGet, s.handleAdminListAudit))
	adminMux.HandleFunc("/api/admin/devices", methodOnly(http.MethodGet, s.handleAdminListDevices))
	adminMux.HandleFunc("/api/admin/devices/", s.handleAdminDeviceItem)
	adminMux.HandleFunc("/api/admin/services", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.handleAdminListServices(w, r)
		case http.MethodPost:
			s.handleAdminCreateService(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "仅支持 GET/POST")
		}
	})
	adminMux.HandleFunc("/api/admin/services/", s.handleAdminServiceItem)
	adminMux.HandleFunc("/api/admin/forwards", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.handleAdminListForwards(w, r)
		case http.MethodPost:
			s.handleAdminCreateForward(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "仅支持 GET/POST")
		}
	})
	adminMux.HandleFunc("/api/admin/forwards/", s.handleAdminForwardItem)
	adminMux.HandleFunc("/api/admin/enroll/tokens", methodOnly(http.MethodPost, s.handleAdminIssueToken))
	mux.Handle("/api/admin/", s.authAdminMiddleware(adminMux))

	// /admin/ 提供嵌入的静态 Web UI；以及 /admin → /admin/ 重定向
	mux.Handle("/admin/", adminWebHandler())
	mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusMovedPermanently)
	})
}
