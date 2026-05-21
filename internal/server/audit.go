package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/zhiying8710/oh-my-lan/internal/auth"
	"github.com/zhiying8710/oh-my-lan/internal/store"
)

// 集中定义 action 常量，避免到处写错。
const (
	ActionDeviceEnroll      = "device.enroll"
	ActionDeviceRevoke      = "device.revoke"
	ActionServiceAdd        = "service.add"
	ActionServiceDelete     = "service.delete"
	ActionServiceEnable     = "service.enable"
	ActionServiceDisable    = "service.disable"
	ActionForwardAdd        = "forward.add"
	ActionForwardDelete     = "forward.delete"
	ActionForwardEnable     = "forward.enable"
	ActionForwardDisable    = "forward.disable"
	ActionEnrollTokenIssue  = "enroll_token.issue"
	ActionAuthLogin         = "auth.login"
	ActionAuthLogout        = "auth.logout"
	ActionAuthUserSet       = "auth.user.set"
	ActionAuthUserDelete    = "auth.user.delete"
)

// audit 写一条审计记录。失败仅 warn 不阻断业务。
//
// detail 可以是 nil 或任意可 JSON marshal 的对象；最常用法：map[string]any。
func (s *Server) audit(ctx context.Context, actor, action, target string, detail any) {
	var detailStr string
	if detail != nil {
		if b, err := json.Marshal(detail); err == nil {
			detailStr = string(b)
		}
	}
	id, _ := auth.NewRandomID()
	err := s.store.WriteAudit(ctx, store.AuditEntry{
		ID:     id,
		TS:     time.Now().UTC(),
		Actor:  actor,
		Action: action,
		Target: target,
		Detail: detailStr,
	})
	if err != nil {
		s.logger.Warn("audit 写入失败", "action", action, "err", err)
	}
}

// adminActor 返回经过 authAdminMiddleware 后注入到 context 的 actor 字符串。
// 取值形如：
//   - "user:<username>"（session 登录）
//   - "admin:<token_hash_short>"（直接 admin token）
//   - "admin:unknown"（理论上不可达，作为防御性 fallback）
func adminActor(r *http.Request) string {
	if v, ok := r.Context().Value(adminActorCtxKey{}).(string); ok && v != "" {
		return v
	}
	return "admin:unknown"
}

func deviceActor(deviceID string) string {
	return "device:" + deviceID
}

