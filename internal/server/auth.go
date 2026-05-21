package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/zhiying8710/oh-my-lan/internal/auth"
	"github.com/zhiying8710/oh-my-lan/internal/store"
)

// 认证方案：客户端在 Authorization 头携带 "Bearer <device_id>.<tunnel_secret>"。
// 之所以用点号合并而非 user:pass 风格，是避免 base64 编码这一层冗余。
const bearerPrefix = "Bearer "

type deviceCtxKey struct{}

var errAuthRequired = errors.New("缺少或非法的 Authorization 头")

func deviceFromContext(ctx context.Context) (store.Device, bool) {
	d, ok := ctx.Value(deviceCtxKey{}).(store.Device)
	return d, ok
}

// authDeviceMiddleware 验证 Bearer 凭证，把 store.Device 注入 context。
func (s *Server) authDeviceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dev, err := s.authenticateDevice(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		ctx := context.WithValue(r.Context(), deviceCtxKey{}, dev)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) authenticateDevice(r *http.Request) (store.Device, error) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, bearerPrefix) {
		return store.Device{}, errAuthRequired
	}
	tok := strings.TrimPrefix(h, bearerPrefix)
	parts := strings.SplitN(tok, ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return store.Device{}, errAuthRequired
	}
	deviceID, secret := parts[0], parts[1]

	dev, err := s.store.GetDeviceByID(r.Context(), deviceID)
	if err != nil {
		// 不区分"device 不存在"和"密码不对"，避免账户枚举。
		return store.Device{}, errAuthRequired
	}
	if !auth.ConstantTimeEqual(dev.TunnelSecret, secret) {
		return store.Device{}, errAuthRequired
	}
	return dev, nil
}

// requireLocal 把 handler 限定为只接受 127.0.0.1 / ::1 来源的请求。
// 用于 token 签发等只该在服务端本机使用的端点。
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
			writeError(w, http.StatusForbidden, "该端点仅允许本机调用")
		}
	}
}
