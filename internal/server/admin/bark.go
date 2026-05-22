// admin/bark.go: bark 推送配置 admin handler。三个端点：
//   GET  /api/admin/bark         读当前设置
//   PUT  /api/admin/bark         更新设置（启用时校验 URL 形态）
//   POST /api/admin/bark/test    立即触发一次推送（验证 URL 可达）
//
// 非 handler 部分（sendBarkPush / formatRelativeTime）下沉到 internal/server/api/，
// 因为 server lifecycle 的 offline reaper 也要用。

package admin

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/zhiying8710/oh-my-lan/internal/proto"
	"github.com/zhiying8710/oh-my-lan/internal/server/api"
	"github.com/zhiying8710/oh-my-lan/internal/store"
)

// GET /api/admin/bark
func (h *Handler) HandleAdminBarkGet(w http.ResponseWriter, r *http.Request) {
	bs, err := h.Store.GetBarkSettings(r.Context())
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	api.WriteJSON(w, http.StatusOK, proto.BarkSettingsDTO{
		Enabled:                 bs.Enabled,
		BarkURL:                 bs.BarkURL,
		OfflineThresholdSeconds: bs.OfflineThresholdSeconds,
	})
}

// PUT /api/admin/bark
func (h *Handler) HandleAdminBarkPut(w http.ResponseWriter, r *http.Request) {
	var req proto.BarkSettingsDTO
	if err := api.DecodeJSON(r, &req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "请求体非法 JSON: "+err.Error())
		return
	}
	if req.OfflineThresholdSeconds < 30 {
		req.OfflineThresholdSeconds = 30 // 防止过于敏感
	}
	barkURL := strings.TrimSpace(req.BarkURL)
	// 启用时校验 URL 形态：scheme + host 都必须存在，避免存 garbage 到 push 时才发现
	if req.Enabled {
		u, err := url.Parse(barkURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			api.WriteError(w, http.StatusBadRequest, "bark URL 必须是 http(s)://host/path 形式")
			return
		}
	}
	bs := store.BarkSettings{
		Enabled:                 req.Enabled,
		BarkURL:                 barkURL,
		OfflineThresholdSeconds: req.OfflineThresholdSeconds,
	}
	if err := h.Store.SetBarkSettings(r.Context(), bs); err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.Auditor.Write(r.Context(), api.AdminActor(r), api.ActionBarkConfigure, "", map[string]any{
		"enabled":   bs.Enabled,
		"threshold": bs.OfflineThresholdSeconds,
	})
	api.WriteJSON(w, http.StatusOK, req)
}

// POST /api/admin/bark/test —— 让用户在 UI 上点"测试推送"立刻验证 URL 可达
func (h *Handler) HandleAdminBarkTest(w http.ResponseWriter, r *http.Request) {
	bs, err := h.Store.GetBarkSettings(r.Context())
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if bs.BarkURL == "" {
		api.WriteError(w, http.StatusBadRequest, "bark URL 未配置")
		return
	}
	if err := api.SendBarkPush(r.Context(), bs.BarkURL,
		"oh-my-lan · 测试推送", "如果你看到这条消息，bark 推送已就绪 ✓"); err != nil {
		// 写 audit：暴破/钓鱼也会触发测试推送频繁失败，留痕
		h.Auditor.Write(r.Context(), api.AdminActor(r), api.ActionBarkTest, "",
			map[string]any{"ok": false, "err": err.Error()})
		api.WriteError(w, http.StatusBadGateway, "推送失败: "+err.Error())
		return
	}
	h.Auditor.Write(r.Context(), api.AdminActor(r), api.ActionBarkTest, "", map[string]any{"ok": true})
	w.WriteHeader(http.StatusNoContent)
}
