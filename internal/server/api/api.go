// Package api 是 internal/server 包族的"叶子共享层"：HTTP 响应工具、DTO mapper、
// audit、ctx keys、middleware、PortAllocator、RateLimiter。
//
// 拆出来的动机：拆 god-package 时，{admin,device,auth} 三个 handler 子包都要用到
// audit/writeJSON/ctx keys/middleware 这套基础设施。把它们留在 package server 会形成
// "server → 子包 → server" 的导入环；唯一的非环结构是：api 是叶子，所有子包 + server
// 都导入它，没有反向依赖。
//
// 设计取舍：
//   - 没拆得更细（如 api/audit、api/http）：当前总量小（~250 LOC），细拆只会让 import
//     列表更长。等真有第三方调用方时再切。
//   - 多数 helper 保持原行为，只是名字首字母大写以便跨包调用。
//   - 没引 interface：handler 子包通过具体 *store.Store / *tunnel.Server 拿依赖，简单
//     直接；想 mock 时改写 store/tunnel 自身的接口，而不是这里再加一层抽象。
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zhiying8710/oh-my-lan/internal/auth"
	"github.com/zhiying8710/oh-my-lan/internal/proto"
	"github.com/zhiying8710/oh-my-lan/internal/store"
)

// ---------------- HTTP helpers ----------------

func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, proto.ErrorResponse{Error: msg})
}

func DecodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// MethodOnly 限制路由仅接受特定 HTTP 方法。
func MethodOnly(method string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			WriteError(w, http.StatusMethodNotAllowed, "仅支持 "+method)
			return
		}
		h(w, r)
	}
}

// WithCORS 给所有 /api/* 响应加 CORS headers，让非同源前端（如 Tauri webview
// 的 tauri://localhost）能正常 fetch。
//
// 安全考虑：admin 与 device 端点都用 Authorization: Bearer 头认证，不依赖 cookie；
// 因此放开 Access-Control-Allow-Origin: * 不引入额外风险。
func WithCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		// PUT 必须列出——/api/admin/bark 用 PUT。历史教训：早期只写 GET/POST/DELETE/OPTIONS，
		// Tauri webview 跨源调 PUT 触发预检，server 没声明 PUT → 浏览器直接 reject 实际请求，
		// fetch 抛 "Load failed"（看起来像网络错误，实际是 CORS 预检失败）。PATCH 同理预留。
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Max-Age", "600")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func RemoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---------------- DTO mappers ----------------

func ToServiceDTO(sv store.Service) proto.ServiceDTO {
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

// WriteForwardDTO 把 forward 解引用为 ForwardDTO 后写出（用于 enable/disable 后返回最新状态）。
// 抽到 api/ 作为自由函数，避免在 admin/ 和 device/ 各保留一份语义相同但 receiver 不同的拷贝。
// 仅 2 次 query（GetService + GetDevice），不在循环中调用，不构成 N+1。
func WriteForwardDTO(w http.ResponseWriter, r *http.Request, st *store.Store, f store.Forward) {
	remote, err := st.GetService(r.Context(), f.RemoteServiceID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	owner, err := st.GetDeviceByID(r.Context(), remote.DeviceID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, ToForwardDTO(f, remote, owner))
}

func ToForwardDTO(f store.Forward, remote store.Service, remoteOwner store.Device) proto.ForwardDTO {
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

// NormalizeLocalAddr 把用户输入的 "本地服务地址" 规范化成 host:port。详见原 helpers.go 注释。
func NormalizeLocalAddr(in string) (string, error) {
	in = strings.TrimSpace(in)
	if in == "" {
		return "", fmt.Errorf("local_addr 不能为空")
	}
	if port, err := strconv.Atoi(in); err == nil {
		if port < 1 || port > 65535 {
			return "", fmt.Errorf("端口 %d 必须在 1-65535", port)
		}
		return fmt.Sprintf("127.0.0.1:%d", port), nil
	}
	if strings.HasPrefix(in, ":") {
		port, err := strconv.Atoi(in[1:])
		if err != nil || port < 1 || port > 65535 {
			return "", fmt.Errorf("端口非法: %q", in)
		}
		return fmt.Sprintf("127.0.0.1:%d", port), nil
	}
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
	return net.JoinHostPort(host, portStr), nil
}

// ---------------- ctx keys + actor helpers ----------------

// CTX 注入约定：device middleware 把 store.Device 整体塞进 ctx；admin middleware 把
// "actor 字符串" + 可选 "登录用户 ID" 塞进 ctx。
// 这些 key 是 unexported struct{} 类型——既不和别人的 context 冲突，也不让外部模块
// 偷偷塞值。
type DeviceCtxKey struct{}
type AdminActorCtxKey struct{}
type AuthedUserIDCtxKey struct{}

// DeviceFromContext 取出 device middleware 注入的 store.Device。
func DeviceFromContext(ctx context.Context) (store.Device, bool) {
	d, ok := ctx.Value(DeviceCtxKey{}).(store.Device)
	return d, ok
}

// AdminActor 返回经过 AuthAdminMiddleware 后注入的 actor 字符串。
//   - "user:<username>"（session 登录）
//   - "admin:<token_hash_short>"（直接 admin token）
//   - "admin:unknown"（理论上不可达，作为防御性 fallback）
func AdminActor(r *http.Request) string {
	if v, ok := r.Context().Value(AdminActorCtxKey{}).(string); ok && v != "" {
		return v
	}
	return "admin:unknown"
}

func DeviceActor(deviceID string) string { return "device:" + deviceID }

// WithActor 把 actor + 可选用户 ID 注入 context。
func WithActor(ctx context.Context, actor, userID string) context.Context {
	ctx = context.WithValue(ctx, AdminActorCtxKey{}, actor)
	if userID != "" {
		ctx = context.WithValue(ctx, AuthedUserIDCtxKey{}, userID)
	}
	return ctx
}

// AuthedUserID 取 session-登录用户 ID（admin_token 路径下为空）。
func AuthedUserID(ctx context.Context) string {
	v, _ := ctx.Value(AuthedUserIDCtxKey{}).(string)
	return v
}

// ---------------- Audit ----------------

// Audit action 常量。集中放避免到处写错。
const (
	ActionDeviceEnroll     = "device.enroll"
	ActionDeviceRevoke     = "device.revoke"
	ActionServiceAdd       = "service.add"
	ActionServiceDelete    = "service.delete"
	ActionServiceEnable    = "service.enable"
	ActionServiceDisable   = "service.disable"
	ActionForwardAdd       = "forward.add"
	ActionForwardDelete    = "forward.delete"
	ActionForwardEnable    = "forward.enable"
	ActionForwardDisable   = "forward.disable"
	ActionEnrollTokenIssue = "enroll_token.issue"
	ActionAuthLogin        = "auth.login"
	ActionAuthLogout       = "auth.logout"
	ActionAuthUserSet      = "auth.user.set"
	ActionAuthUserDelete   = "auth.user.delete"
	ActionBarkConfigure    = "bark.configure"
	ActionBarkTest         = "bark.test"
	ActionAuthRateLimited  = "auth.rate_limited"
)

// Auditor 把"写一条审计"的依赖（store + logger）抽到一个对象，
// handler 子包通过 Deps.Auditor 调用。失败仅 warn，不阻断业务。
type Auditor struct {
	Store  *store.Store
	Logger *slog.Logger
}

func (a *Auditor) Write(ctx context.Context, actor, action, target string, detail any) {
	var detailStr string
	if detail != nil {
		if b, err := json.Marshal(detail); err == nil {
			detailStr = string(b)
		}
	}
	id, _ := auth.NewRandomID()
	err := a.Store.WriteAudit(ctx, store.AuditEntry{
		ID: id, TS: time.Now().UTC(),
		Actor: actor, Action: action, Target: target, Detail: detailStr,
	})
	if err != nil {
		a.Logger.Warn("audit 写入失败", "action", action, "err", err)
	}
}

// ---------------- Port allocator ----------------

var ErrPortPoolExhausted = errors.New("公网端口池已用尽")

type PortAllocator struct {
	min, max int
	store    *store.Store
	mu       sync.Mutex
}

func NewPortAllocator(s *store.Store, min, max int) *PortAllocator {
	return &PortAllocator{min: min, max: max, store: s}
}

// Min/Max getter——admin handler 暴露 metrics 用到。
func (p *PortAllocator) Min() int { return p.min }
func (p *PortAllocator) Max() int { return p.max }

func (p *PortAllocator) Allocate(ctx context.Context) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	used, err := p.store.UsedPublicPorts(ctx)
	if err != nil {
		return 0, err
	}
	taken := make(map[int]struct{}, len(used))
	for _, port := range used {
		taken[port] = struct{}{}
	}
	for port := p.min; port <= p.max; port++ {
		if _, ok := taken[port]; !ok {
			return port, nil
		}
	}
	return 0, ErrPortPoolExhausted
}

// ---------------- Rate limiter ----------------

// LoginRateLimiter 是内存级 IP→失败计数器，专门给 /api/auth/login 用。
// 详细设计取舍见原 ratelimit.go。
type LoginRateLimiter struct {
	mu       sync.Mutex
	failures map[string][]time.Time
	maxFail  int
	window   time.Duration
}

func NewLoginRateLimiter() *LoginRateLimiter {
	return &LoginRateLimiter{
		failures: make(map[string][]time.Time),
		maxFail:  5,
		window:   time.Minute,
	}
}

// Allow 检查给定 IP 是否仍在配额内。本身不计数。
func (r *LoginRateLimiter) Allow(ip string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := time.Now().Add(-r.window)
	r.gcLocked(ip, cutoff)
	return len(r.failures[ip]) < r.maxFail
}

func (r *LoginRateLimiter) RecordFailure(ip string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures[ip] = append(r.failures[ip], time.Now())
}

func (r *LoginRateLimiter) gcLocked(ip string, cutoff time.Time) {
	arr := r.failures[ip]
	keep := arr[:0]
	for _, t := range arr {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		delete(r.failures, ip)
	} else {
		r.failures[ip] = keep
	}
}

// ---------------- Bark push helpers ----------------

// barkHTTPClient 是 bark 推送的全局 HTTP client。单 timeout，标准库够用。
// Bark 是简单的 GET/POST 服务，没必要拖个 SDK。
var barkHTTPClient = &http.Client{Timeout: 10 * time.Second}

// SendBarkPush 向 base URL 推一条消息。base 形如 https://api.day.app/<device_key>。
// 失败返回 error，调用方决定是否重试 / 写 audit。
//
// 既被 admin/ 的"测试推送"按钮端点用，也被 server lifecycle 的 offline reaper 调，
// 因此提到 api/ 作为共享叶子。
func SendBarkPush(ctx context.Context, base, title, body string) error {
	base = strings.TrimRight(base, "/")
	if base == "" {
		return errors.New("bark URL 为空")
	}
	endpoint := fmt.Sprintf("%s/%s/%s",
		base,
		urlPathEscape(title),
		urlPathEscape(body),
	)
	q := "group=oh-my-lan&level=active"
	endpoint = endpoint + "?" + q

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := barkHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("bark 返回 HTTP %d", resp.StatusCode)
	}
	return nil
}

// urlPathEscape 是 url.PathEscape 的本地包装，避免 api 包导入 net/url 仅为一次调用。
// 直接用 strings 模拟 path-escape 行为：保留 ASCII letter/digit/常用 path 安全字符，其余 percent-encode。
// 该函数对中文/空格/标点都做 percent encoding。
func urlPathEscape(s string) string {
	const safe = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~!$&'()*+,;=:@"
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if strings.IndexByte(safe, c) >= 0 {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			const hex = "0123456789ABCDEF"
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0xF])
		}
	}
	return b.String()
}

// FormatRelativeTime 把 time.Time 转成 "X 分钟前 / X 小时前" 的中文描述。
// IsZero → "从未上线"。bark 推送和 UI 都用。
func FormatRelativeTime(t time.Time) string {
	if t.IsZero() {
		return "从未上线"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d 秒前", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	default:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	}
}

// ---------------- Auth middleware ----------------

const bearerPrefix = "Bearer "

// ErrAuthRequired 是所有 device/admin 鉴权失败的统一原因（不区分 missing/bad/expired，防枚举）。
var ErrAuthRequired = errors.New("缺少或非法的 Authorization 头")

// AuthenticateDevice 解析 Bearer "<id>.<secret>" 并校验。成功返回 store.Device。
// 业务 handler 通常不直接调，走 AuthDeviceMiddleware；测试与 admin revoke 路径直接用。
func AuthenticateDevice(ctx context.Context, st *store.Store, r *http.Request) (store.Device, error) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, bearerPrefix) {
		return store.Device{}, ErrAuthRequired
	}
	tok := strings.TrimPrefix(h, bearerPrefix)
	parts := strings.SplitN(tok, ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return store.Device{}, ErrAuthRequired
	}
	deviceID, secret := parts[0], parts[1]

	dev, err := st.GetDeviceByID(ctx, deviceID)
	if err != nil {
		return store.Device{}, ErrAuthRequired
	}
	if !auth.ConstantTimeEqual(dev.TunnelSecret, secret) {
		return store.Device{}, ErrAuthRequired
	}
	return dev, nil
}

// AuthDeviceMiddleware 验证 Bearer 凭证，把 store.Device 注入 context。
func AuthDeviceMiddleware(st *store.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dev, err := AuthenticateDevice(r.Context(), st, r)
		if err != nil {
			WriteError(w, http.StatusUnauthorized, err.Error())
			return
		}
		ctx := context.WithValue(r.Context(), DeviceCtxKey{}, dev)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ExtractAdminToken 从 Authorization 头或 oml_admin cookie 中取出 raw token。
// Bearer 优先；找不到再看 cookie。
func ExtractAdminToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, bearerPrefix) {
		return strings.TrimPrefix(h, bearerPrefix)
	}
	c, err := r.Cookie("oml_admin")
	if err == nil && c.Value != "" {
		return c.Value
	}
	return ""
}

// AuthAdminMiddleware 兼容三种 admin 凭证：
//   1. session token（用户登录后拿到，sess_ 前缀）
//   2. admin_token（CLI 直接颁发的机器凭证）
//   3. 都不匹配 → 401
// 成功时 inject "actor + 可选用户 ID" 到 ctx。
func AuthAdminMiddleware(st *store.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := ExtractAdminToken(r)
		if raw == "" {
			WriteError(w, http.StatusUnauthorized, ErrAuthRequired.Error())
			return
		}
		hash := auth.HashSecret(raw)
		// session 路径
		if sess, err := st.GetActiveSessionByHash(r.Context(), hash, time.Now()); err == nil {
			user, err := st.GetAdminUserByID(r.Context(), sess.UserID)
			if err == nil {
				_ = st.TouchSession(r.Context(), sess.ID, time.Now())
				ctx := WithActor(r.Context(), "user:"+user.Username, user.ID)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}
		// admin_token 路径
		if _, err := st.GetAdminTokenByHash(r.Context(), hash); err == nil {
			// token hash 截前 8 字符做 actor 显示——既不暴露完整 hash 又能在审计里区分多个 token
			short := hash
			if len(short) > 8 {
				short = short[:8]
			}
			ctx := WithActor(r.Context(), "admin:"+short, "")
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		WriteError(w, http.StatusUnauthorized, ErrAuthRequired.Error())
	})
}
