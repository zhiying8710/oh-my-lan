package server

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/zhiying8710/oh-my-lan/internal/proto"
	"github.com/zhiying8710/oh-my-lan/internal/store"
)

// 本文件集中放与 handler 无关的公用工具：HTTP 响应、DTO mapper、路由 helper。
// 拆出来是为了让 handler_device / handler_admin 保持纯业务逻辑。

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, proto.ErrorResponse{Error: msg})
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// methodOnly 限制路由仅接受特定 HTTP 方法。
func methodOnly(method string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			writeError(w, http.StatusMethodNotAllowed, "仅支持 "+method)
			return
		}
		h(w, r)
	}
}

// withCORS 给所有 /api/* 响应加 CORS headers，让非同源前端（如 Tauri webview
// 的 tauri://localhost）能正常 fetch。
//
// 安全考虑：admin 与 device 端点都用 Authorization: Bearer 头认证，不依赖 cookie；
// 因此放开 Access-Control-Allow-Origin: * 不引入额外风险。
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Max-Age", "600")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---------------- DTO mappers ----------------

func toServiceDTO(sv store.Service) proto.ServiceDTO {
	return proto.ServiceDTO{
		ID:         sv.ID,
		DeviceID:   sv.DeviceID,
		Name:       sv.Name,
		Protocol:   sv.Protocol,
		LocalAddr:  sv.LocalAddr,
		PublicPort: sv.PublicPort,
		Enabled:    sv.Enabled,
		CreatedAt:  sv.CreatedAt,
	}
}

func toForwardDTO(f store.Forward, remote store.Service, remoteOwner store.Device) proto.ForwardDTO {
	return proto.ForwardDTO{
		ID:                f.ID,
		OwnerDeviceID:     f.OwnerDeviceID,
		RemoteServiceID:   f.RemoteServiceID,
		RemoteDeviceID:    remote.DeviceID,
		RemoteServiceName: remote.Name,
		LocalPort:         f.LocalPort,
		RemotePublicPort:  remote.PublicPort,
		Protocol:          remote.Protocol,
		Enabled:           f.Enabled,
		CreatedAt:         f.CreatedAt,
	}
}

// normalizeLocalAddr 把用户输入的 "本地服务地址" 规范化成 host:port。
//
// 接受形态：
//   - "22"            → "127.0.0.1:22"        （纯端口号，最方便）
//   - ":22"           → "127.0.0.1:22"        （兼容 Go listen 风格）
//   - "127.0.0.1:22"  → 原样
//   - "192.168.1.5:22" → 原样
//   - "[::1]:22"      → 原样
//   - "host.local:80" → 原样
//
// 校验：端口必须是 1-65535 之间的数字；host 不强求是 IP（允许域名）。
func normalizeLocalAddr(in string) (string, error) {
	in = strings.TrimSpace(in)
	if in == "" {
		return "", fmt.Errorf("local_addr 不能为空")
	}
	// 纯数字端口
	if port, err := strconv.Atoi(in); err == nil {
		if port < 1 || port > 65535 {
			return "", fmt.Errorf("端口 %d 必须在 1-65535", port)
		}
		return fmt.Sprintf("127.0.0.1:%d", port), nil
	}
	// 形如 ":22"
	if strings.HasPrefix(in, ":") {
		port, err := strconv.Atoi(in[1:])
		if err != nil || port < 1 || port > 65535 {
			return "", fmt.Errorf("端口非法: %q", in)
		}
		return fmt.Sprintf("127.0.0.1:%d", port), nil
	}
	// 走 host:port 解析（兼容 IPv6 字面量带方括号）
	host, portStr, err := net.SplitHostPort(in)
	if err != nil {
		return "", fmt.Errorf("local_addr 格式非法 %q（期望 host:port、:port 或纯端口号）: %w", in, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("端口非法: %q", portStr)
	}
	if host == "" {
		host = "127.0.0.1"
	}
	// IPv6 字面量回原样（SplitHostPort 已剥掉方括号；JoinHostPort 会加回）
	return net.JoinHostPort(host, portStr), nil
}
