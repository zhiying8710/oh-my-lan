package server

// auth.go: server pkg 内的 middleware/helper 三件套，全部委托给 internal/server/api/。
//
// 拆 god-package 时大部分 (deviceCtxKey、authenticateDevice、Action* 常量、withActor) 已下沉。
// 这里保留的三个都是 router.go 在 wire 路由时直接调用的：方法形式让调用方写
// `s.authDeviceMiddleware(devMux)` 比 `api.AuthDeviceMiddleware(s.store, devMux)` 简洁。

import (
	"net/http"
	"strings"

	"github.com/zhiying8710/oh-my-lan/internal/server/api"
)

// authDeviceMiddleware 委托 api.AuthDeviceMiddleware；router.go 用 6 处。
func (s *Server) authDeviceMiddleware(next http.Handler) http.Handler {
	return api.AuthDeviceMiddleware(s.store, next)
}

// authAdminMiddleware 委托 api.AuthAdminMiddleware；router.go 用 1 处（/api/auth/{logout,me}）。
// 注意：/api/admin/* 路由由 admin.Handler.AuthMiddleware 直接负责，不走这里。
func (s *Server) authAdminMiddleware(next http.Handler) http.Handler {
	return api.AuthAdminMiddleware(s.store, next)
}

// requireLocal 把 handler 限定为只接受 127.0.0.1 / ::1 来源的请求。
// 用于 token 签发等"只该在服务端本机使用"的端点。router.go /api/enroll/tokens 用 1 处。
func requireLocal(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host := r.RemoteAddr
		if i := strings.LastIndex(host, ":"); i >= 0 {
			host = host[:i]
		}
		host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
		switch host {
		case "127.0.0.1", "::1", "localhost":
			next(w, r)
		default:
			api.WriteError(w, http.StatusForbidden, "该端点仅允许本机调用")
		}
	}
}
