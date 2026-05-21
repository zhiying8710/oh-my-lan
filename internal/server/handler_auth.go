package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/zhiying8710/oh-my-lan/internal/auth"
	"github.com/zhiying8710/oh-my-lan/internal/proto"
	"github.com/zhiying8710/oh-my-lan/internal/store"
)

// 全局可调；保持简单不进 config，常量足够。
const sessionTTL = 7 * 24 * time.Hour

// POST /api/auth/login
//
// 不要求已认证。校验用户名+密码后签发 session token。
// 错误故意只回笼统 "用户名或密码错误"，不区分是用户不存在还是密码错（防枚举）。
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	var req proto.LoginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体非法 JSON: "+err.Error())
		return
	}
	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "用户名和密码必填")
		return
	}

	user, err := s.store.GetAdminUserByUsername(r.Context(), req.Username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "用户名或密码错误")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := auth.VerifyPassword(req.Password, user.PasswordHash); err != nil {
		writeError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}

	raw, err := auth.NewSessionToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	id, err := auth.NewRandomID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now().UTC()
	sess := store.Session{
		ID:        id,
		UserID:    user.ID,
		TokenHash: auth.HashSecret(raw),
		CreatedAt: now,
		ExpiresAt: now.Add(sessionTTL),
	}
	if err := s.store.CreateSession(r.Context(), sess); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r.Context(), "user:"+user.Username, ActionAuthLogin, user.ID, nil)
	writeJSON(w, http.StatusOK, proto.LoginResponse{
		SessionToken: raw,
		User: proto.AdminUserDTO{
			ID: user.ID, Username: user.Username, CreatedAt: user.CreatedAt,
		},
		ExpiresAt: sess.ExpiresAt,
	})
}

// POST /api/auth/logout
//
// 删除当前 session（用 bearer 反查）。无 session 也回 204（幂等）。
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	raw := extractAdminToken(r)
	if raw != "" {
		_ = s.store.DeleteSessionByHash(r.Context(), auth.HashSecret(raw))
	}
	if actor, ok := r.Context().Value(adminActorCtxKey{}).(string); ok {
		s.audit(r.Context(), actor, ActionAuthLogout, "", nil)
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/auth/me  (需要登录)
func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(authedUserIDCtxKey{}).(string)
	if uid == "" {
		// 走的是 admin_token 路径（不绑定用户）；返回 admin token 标识
		writeJSON(w, http.StatusOK, proto.MeResponse{
			User: proto.AdminUserDTO{Username: "(admin_token)"},
		})
		return
	}
	u, err := s.store.GetAdminUserByID(r.Context(), uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, proto.MeResponse{
		User: proto.AdminUserDTO{ID: u.ID, Username: u.Username, CreatedAt: u.CreatedAt},
	})
}

// ---------------- 工具函数 ----------------

type adminActorCtxKey struct{}
type authedUserIDCtxKey struct{}

// withActor 把 actor 字符串和（可选的）用户 ID 注入 context，给 handler 与 audit 用。
func withActor(ctx context.Context, actor, userID string) context.Context {
	ctx = context.WithValue(ctx, adminActorCtxKey{}, actor)
	if userID != "" {
		ctx = context.WithValue(ctx, authedUserIDCtxKey{}, userID)
	}
	return ctx
}
