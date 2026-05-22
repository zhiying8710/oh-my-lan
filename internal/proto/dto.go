// Package proto 定义控制平面 HTTP API 的请求/响应 DTO。
// 这些类型同时被服务端 handler 和客户端 api_client 引用，保证 schema 一致。
package proto

import "time"

// ErrorResponse 是所有失败响应的统一形态。
type ErrorResponse struct {
	Error string `json:"error"`
}

// ---------------- Enrollment ----------------

type IssueTokenRequest struct {
	TTLSeconds int `json:"ttl_seconds,omitempty"` // 0 表示用服务端默认
}

type IssueTokenResponse struct {
	ID        string    `json:"id"`
	Token     string    `json:"token"` // 明文，仅生成一次
	ExpiresAt time.Time `json:"expires_at"`
}

type EnrollDeviceRequest struct {
	Token      string `json:"token"`
	DeviceName string `json:"device_name"`
}

type EnrollDeviceResponse struct {
	DeviceID          string `json:"device_id"`
	DeviceName        string `json:"device_name"`
	TunnelSecret      string `json:"tunnel_secret"`      // 明文，仅返回一次
	ServerFingerprint string `json:"server_fingerprint"` // chisel server SSH 指纹
	ChiselAddr        string `json:"chisel_addr"`        // 客户端 daemon 应连接的 chisel 入口，例如 "vps.example.com:8443"
}

// ---------------- Service ----------------

type AddServiceRequest struct {
	Name      string `json:"name"`
	Protocol  string `json:"protocol"`   // tcp / udp
	LocalAddr string `json:"local_addr"` // 例如 "127.0.0.1:22"
}

type ServiceDTO struct {
	ID         string    `json:"id"`
	DeviceID   string    `json:"device_id"`
	Name       string    `json:"name"`
	Protocol   string    `json:"protocol"`
	LocalAddr  string    `json:"local_addr"`
	PublicPort int       `json:"public_port"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
}

type ListServicesResponse struct {
	Services []ServiceDTO `json:"services"`
}

// ---------------- Bootstrap ----------------

// BootstrapResponse 是 daemon 启动时拉取的"我应该建立哪些隧道"清单。
// Remotes：本设备发布的服务（chisel R: spec 数据来源）
// Locals：本设备消费的他人服务 forward 规则（chisel L: spec 数据来源）
type BootstrapResponse struct {
	ServerFingerprint string        `json:"server_fingerprint"`
	ChiselAddr        string        `json:"chisel_addr"`
	Remotes           []RemoteEntry `json:"remotes"`
	Locals            []LocalEntry  `json:"locals"`
}

type RemoteEntry struct {
	ServiceID  string `json:"service_id"`
	PublicPort int    `json:"public_port"`
	LocalAddr  string `json:"local_addr"`
	Protocol   string `json:"protocol"`
}

// LocalEntry 描述本机要起的 L: forward listener。
// LocalPort：本机监听端口（127.0.0.1:LocalPort）
// RemotePublicPort：在 chisel server 上拨号的目标端口（即对端 service 的 public_port）
// Protocol：tcp / udp
type LocalEntry struct {
	ForwardID        string `json:"forward_id"`
	LocalPort        int    `json:"local_port"`
	RemotePublicPort int    `json:"remote_public_port"`
	Protocol         string `json:"protocol"`
}

// ---------------- Forward CRUD ----------------

type AddForwardRequest struct {
	RemoteServiceID string `json:"remote_service_id"`
	LocalPort       int    `json:"local_port"`
}

type ForwardDTO struct {
	ID               string    `json:"id"`
	OwnerDeviceID    string    `json:"owner_device_id"`
	RemoteServiceID  string    `json:"remote_service_id"`
	RemoteDeviceID   string    `json:"remote_device_id"`
	RemoteServiceName string   `json:"remote_service_name"`
	LocalPort        int       `json:"local_port"`
	RemotePublicPort int       `json:"remote_public_port"`
	Protocol         string    `json:"protocol"`
	Enabled          bool      `json:"enabled"`
	CreatedAt        time.Time `json:"created_at"`
}

type ListForwardsResponse struct {
	Forwards []ForwardDTO `json:"forwards"`
}

// ---------------- Cross-device service listing ----------------

// ServiceBriefDTO 在跨设备 listing 中暴露最小信息：让 A 知道 B 的服务存在，但不暴露 device 内部 metadata。
type ServiceBriefDTO struct {
	ID         string `json:"id"`
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	Name       string `json:"name"`
	Protocol   string `json:"protocol"`
	PublicPort int    `json:"public_port"`
	Enabled    bool   `json:"enabled"`
}

type ListAllServicesResponse struct {
	Services []ServiceBriefDTO `json:"services"`
}

// ---------------- Admin (Web UI) ----------------

type AdminInfoResponse struct {
	ServerFingerprint string `json:"server_fingerprint"`
	ChiselAddr        string `json:"chisel_addr"`
	PortPoolMin       int    `json:"port_pool_min"`
	PortPoolMax       int    `json:"port_pool_max"`
	Version           string `json:"version"`
}

type AdminDeviceDTO struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Status        string     `json:"status"`
	LastSeenAt    *time.Time `json:"last_seen_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	ServicesCount int        `json:"services_count"`
	ForwardsCount int        `json:"forwards_count"`
}

type AdminServiceDTO struct {
	ID         string    `json:"id"`
	DeviceID   string    `json:"device_id"`
	DeviceName string    `json:"device_name"`
	Name       string    `json:"name"`
	Protocol   string    `json:"protocol"`
	LocalAddr  string    `json:"local_addr"`
	PublicPort int       `json:"public_port"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
	// A1' 链路健康：最近一次 server-side TCP dial 探测结果。
	// LastProbeAt 为 nil → 从未探测过；OK=false → 最近探测失败。
	LastProbeAt *time.Time `json:"last_probe_at,omitempty"`
	LastProbeOK bool       `json:"last_probe_ok"`
}

type AdminForwardDTO struct {
	ID                string    `json:"id"`
	OwnerDeviceID     string    `json:"owner_device_id"`
	OwnerDeviceName   string    `json:"owner_device_name"`
	RemoteServiceID   string    `json:"remote_service_id"`
	RemoteServiceName string    `json:"remote_service_name"`
	RemoteDeviceID    string    `json:"remote_device_id"`
	RemoteDeviceName  string    `json:"remote_device_name"`
	LocalPort         int       `json:"local_port"`
	RemotePublicPort  int       `json:"remote_public_port"`
	Protocol          string    `json:"protocol"`
	Enabled           bool      `json:"enabled"`
	CreatedAt         time.Time `json:"created_at"`
}

type AdminListDevicesResponse struct {
	Devices []AdminDeviceDTO `json:"devices"`
}

type AdminListServicesResponse struct {
	Services []AdminServiceDTO `json:"services"`
}

type AdminListForwardsResponse struct {
	Forwards []AdminForwardDTO `json:"forwards"`
}

// 写操作 DTO — admin 代发设备执行的请求。

type AdminAddServiceRequest struct {
	DeviceID  string `json:"device_id"`
	Name      string `json:"name"`
	Protocol  string `json:"protocol"`
	LocalAddr string `json:"local_addr"`
}

type AdminAddForwardRequest struct {
	OwnerDeviceID   string `json:"owner_device_id"`
	RemoteServiceID string `json:"remote_service_id"`
	LocalPort       int    `json:"local_port"`
}

// ---------------- Observability ----------------

type AdminMetricsResponse struct {
	DevicesTotal     int   `json:"devices_total"`
	DevicesOnline    int   `json:"devices_online"`
	ServicesTotal    int   `json:"services_total"`
	ServicesEnabled  int   `json:"services_enabled"`
	ForwardsTotal    int   `json:"forwards_total"`
	ForwardsEnabled  int   `json:"forwards_enabled"`
	AdminTokensTotal int   `json:"admin_tokens_total"`
	PortPoolUsed     int   `json:"port_pool_used"`
	PortPoolSize     int   `json:"port_pool_size"`
	UptimeSeconds    int64 `json:"uptime_seconds"`
}

type AuditEntryDTO struct {
	ID     string    `json:"id"`
	TS     time.Time `json:"ts"`
	Actor  string    `json:"actor"`
	Action string    `json:"action"`
	Target string    `json:"target,omitempty"`
	Detail string    `json:"detail,omitempty"`
}

type AdminListAuditResponse struct {
	Entries []AuditEntryDTO `json:"entries"`
}

// ---------------- Auth (账号密码登录) ----------------

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginResponse struct {
	SessionToken string    `json:"session_token"` // 明文，仅这一次返回；前端存 localStorage
	User         AdminUserDTO `json:"user"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type MeResponse struct {
	User AdminUserDTO `json:"user"`
}

type AdminUserDTO struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
}

// BarkSettingsDTO 是 /api/admin/bark 的请求/响应体。
// bark 推送：设备 last_seen_at 超过阈值时由 server-side reaper 推 iOS/macOS。
type BarkSettingsDTO struct {
	Enabled                 bool   `json:"enabled"`
	BarkURL                 string `json:"bark_url"`
	OfflineThresholdSeconds int    `json:"offline_threshold_seconds"`
}

// DiscoverDTO 是 /api/devices/me/discover 的响应体——
// 设备视角看到的 mesh 内可见的其它设备的服务，用于 omlctl service ls --discover。
type DiscoverDTO struct {
	Services []ServiceBriefDTO `json:"services"`
}

// LogEntryDTO 是 /api/admin/logs 返回的单条记录。与 internal/logging.LogEntry 一一对应。
// 重复定义而不 import logging：proto 是叶子包，不依赖业务包。
type LogEntryDTO struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
	Attrs   string    `json:"attrs,omitempty"`
}

type LogsResponse struct {
	Entries []LogEntryDTO `json:"entries"`
}
