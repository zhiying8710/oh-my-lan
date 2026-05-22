// Package auth 实现 /api/auth/* 三个端点：login / logout / me。
//
// 拆出独立包的动机：原 handler_auth.go 与 admin/device handler 共住 godpackage；逻辑上
// 这层是 "登录态本身"，与业务无关，依赖最少（store + LoginRL + Auditor）。先把它单独抽出，
// 与 prober/ 一起验证拆包模式：subpackage 只依赖 internal/server/api/ 共享层 + store。

package auth

import (
	"errors"
	"net/http"
	"time"

	"github.com/zhiying8710/oh-my-lan/internal/auth"
	"github.com/zhiying8710/oh-my-lan/internal/proto"
	"github.com/zhiying8710/oh-my-lan/internal/server/api"
	"github.com/zhiying8710/oh-my-lan/internal/store"
)

// SessionTTL 是 login 后签发的 session token 有效期。
// 保持简单不入 config，常量足够：7 天对桌面客户端是合理的"重新登录"周期。
const SessionTTL = 7 * 24 * time.Hour

// Handler 持有依赖；由 server orchestrator 在 New() 时构造并 wire 路由。
type Handler struct {
	Store    *store.Store
	Auditor  *api.Auditor
	LoginRL  *api.LoginRateLimiter // 可为 nil（测试场景）：nil 时跳过限频
}

// Register 把 /api/auth/* 三个端点注册到 mux。约定：caller 已用 WithCORS 包过。
func (h *Handler) Register(mux *http.ServeMux, requireAuth func(http.Handler) http.Handler) {
	mux.HandleFunc("/api/auth/login", api.MethodOnly(http.MethodPost, h.HandleLogin))
	// logout / me 需要 admin auth（session 或 admin_token）；orchestrator 传 middleware in
	mux.Handle("/api/auth/logout", requireAuth(http.HandlerFunc(api.MethodOnly(http.MethodPost, h.HandleLogout))))
	mux.Handle("/api/auth/me", requireAuth(http.HandlerFunc(api.MethodOnly(http.MethodGet, h.HandleMe))))
}

// HandleLogin: POST /api/auth/login
// 不要求已认证。校验用户名+密码后签发 session token。
// 错误故意只回笼统 "用户名或密码错误"，不区分用户不存在还是密码错（防枚举）。
func (h *Handler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	ip := api.RemoteIP(r)
	// 先检 rate-limit。argon2id 验证慢 + 这一层兜底，VPS 暴露在公网也能扛住基本字典攻击。
	if h.LoginRL != nil && !h.LoginRL.Allow(ip) {
		// 触发限频写 audit，让 admin 能从「服务端」tab 的审计区看到攻击痕迹
		h.Auditor.Write(r.Context(), "ip:"+ip, api.ActionAuthRateLimited, "", nil)
		w.Header().Set("Retry-After", "60")
		api.WriteError(w, http.StatusTooManyRequests, "登录尝试过于频繁，请稍后再试")
		return
	}

	var req proto.LoginRequest
	if err := api.DecodeJSON(r, &req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "请求体非法 JSON: "+err.Error())
		return
	}
	if req.Username == "" || req.Password == "" {
		api.WriteError(w, http.StatusBadRequest, "用户名和密码必填")
		return
	}

	user, err := h.Store.GetAdminUserByUsername(r.Context(), req.Username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			if h.LoginRL != nil {
				h.LoginRL.RecordFailure(ip)
			}
			api.WriteError(w, http.StatusUnauthorized, "用户名或密码错误")
			return
		}
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := auth.VerifyPassword(req.Password, user.PasswordHash); err != nil {
		if h.LoginRL != nil {
			h.LoginRL.RecordFailure(ip)
		}
		api.WriteError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}

	raw, err := auth.NewSessionToken()
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	id, err := auth.NewRandomID()
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now().UTC()
	sess := store.Session{
		ID:        id,
		UserID:    user.ID,
		TokenHash: auth.HashSecret(raw),
		CreatedAt: now,
		ExpiresAt: now.Add(SessionTTL),
	}
	if err := h.Store.CreateSession(r.Context(), sess); err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.Auditor.Write(r.Context(), "user:"+user.Username, api.ActionAuthLogin, user.ID, nil)
	api.WriteJSON(w, http.StatusOK, proto.LoginResponse{
		SessionToken: raw,
		User: proto.AdminUserDTO{
			ID: user.ID, Username: user.Username, CreatedAt: user.CreatedAt,
		},
		ExpiresAt: sess.ExpiresAt,
	})
}

// HandleLogout: POST /api/auth/logout
// 删除当前 session（用 bearer 反查）。无 session 也回 204（幂等）。
func (h *Handler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	raw := api.ExtractAdminToken(r)
	if raw != "" {
		_ = h.Store.DeleteSessionByHash(r.Context(), auth.HashSecret(raw))
	}
	if actor, ok := r.Context().Value(api.AdminActorCtxKey{}).(string); ok {
		h.Auditor.Write(r.Context(), actor, api.ActionAuthLogout, "", nil)
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleMe: GET /api/auth/me  (需要登录)
func (h *Handler) HandleMe(w http.ResponseWriter, r *http.Request) {
	uid := api.AuthedUserID(r.Context())
	if uid == "" {
		// 走的是 admin_token 路径（不绑定用户）；返回 admin token 标识
		api.WriteJSON(w, http.StatusOK, proto.MeResponse{
			User: proto.AdminUserDTO{Username: "(admin_token)"},
		})
		return
	}
	u, err := h.Store.GetAdminUserByID(r.Context(), uid)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	api.WriteJSON(w, http.StatusOK, proto.MeResponse{
		User: proto.AdminUserDTO{ID: u.ID, Username: u.Username, CreatedAt: u.CreatedAt},
	})
}
