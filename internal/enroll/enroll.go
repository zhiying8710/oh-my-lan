// Package enroll 编排"生成一次性 token / 客户端用 token 注册设备"两个流程。
// 它把 internal/auth（随机+hash）和 internal/store（持久化）粘起来，
// 让 HTTP handler 不必关心底层实现细节。
package enroll

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zhiying8710/oh-my-lan/internal/auth"
	"github.com/zhiying8710/oh-my-lan/internal/store"
)

// DefaultTokenTTL 是 enrollment token 默认有效期。
const DefaultTokenTTL = 24 * time.Hour

// 业务级错误，handler 层把它们映射为 HTTP 状态码。
var (
	ErrTokenInvalid = errors.New("enrollment token 无效或已被使用")
	ErrTokenExpired = errors.New("enrollment token 已过期")
	ErrDeviceExists = errors.New("device name 已存在")
)

// Service 暴露 enrollment 的应用层动作。
type Service struct {
	store *store.Store
	now   func() time.Time
}

func New(s *store.Store) *Service {
	return &Service{store: s, now: time.Now}
}

// IssuedToken 是生成 token 后只返回一次的明文 + 元信息。
// token 明文只在此结构里出现一次，调用方应立刻显示给用户并丢弃。
type IssuedToken struct {
	ID        string
	Token     string
	ExpiresAt time.Time
}

// IssueToken 生成一个新的一次性 token。
func (s *Service) IssueToken(ctx context.Context, ttl time.Duration) (IssuedToken, error) {
	if ttl <= 0 {
		ttl = DefaultTokenTTL
	}
	id, err := auth.NewDeviceID() // 复用同样的 16 字节 hex，足够标识 token
	if err != nil {
		return IssuedToken{}, err
	}
	raw, err := auth.NewEnrollmentToken()
	if err != nil {
		return IssuedToken{}, err
	}

	now := s.now().UTC()
	expires := now.Add(ttl)

	if err := s.store.CreateEnrollmentToken(ctx, store.EnrollmentToken{
		ID:        id,
		TokenHash: auth.HashSecret(raw),
		ExpiresAt: expires,
		CreatedAt: now,
	}); err != nil {
		return IssuedToken{}, fmt.Errorf("持久化 token: %w", err)
	}
	return IssuedToken{ID: id, Token: raw, ExpiresAt: expires}, nil
}

// EnrolledDevice 是 enroll 成功后返回给客户端的内容。
// TunnelSecret 是设备隧道密码明文，客户端必须立刻持久化并不要再向服务端索取。
type EnrolledDevice struct {
	DeviceID     string
	DeviceName   string
	TunnelSecret string
}

// EnrollDevice 用 token 注册一台新设备。token 校验通过后：
//   - 生成 device_id + tunnel_secret
//   - 在同一事务里写入 devices 表并把 token 标记为已用
//   - 返回 secret 明文（仅此一次）
func (s *Service) EnrollDevice(ctx context.Context, rawToken, deviceName string) (EnrolledDevice, error) {
	if err := auth.ValidateEnrollTokenFormat(rawToken); err != nil {
		return EnrolledDevice{}, ErrTokenInvalid
	}
	if deviceName == "" {
		return EnrolledDevice{}, fmt.Errorf("device name 不能为空")
	}

	hash := auth.HashSecret(rawToken)
	tok, err := s.store.GetEnrollmentTokenByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return EnrolledDevice{}, ErrTokenInvalid
		}
		return EnrolledDevice{}, fmt.Errorf("查询 token: %w", err)
	}
	if tok.UsedAt != nil {
		return EnrolledDevice{}, ErrTokenInvalid
	}
	if !tok.ExpiresAt.After(s.now()) {
		return EnrolledDevice{}, ErrTokenExpired
	}

	deviceID, err := auth.NewDeviceID()
	if err != nil {
		return EnrolledDevice{}, err
	}
	secret, err := auth.NewTunnelSecret()
	if err != nil {
		return EnrolledDevice{}, err
	}

	dev := store.Device{
		ID:           deviceID,
		Name:         deviceName,
		TunnelSecret: secret,
		Status:       store.DeviceStatusOffline,
		CreatedAt:    s.now().UTC(),
	}

	if err := s.store.ConsumeEnrollmentToken(ctx, hash, dev); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// token 被并发消费掉，或被同进程二次提交。
			return EnrolledDevice{}, ErrTokenInvalid
		}
		if store.IsUniqueViolation(err) {
			return EnrolledDevice{}, ErrDeviceExists
		}
		return EnrolledDevice{}, fmt.Errorf("消费 token: %w", err)
	}
	return EnrolledDevice{
		DeviceID:     deviceID,
		DeviceName:   deviceName,
		TunnelSecret: secret,
	}, nil
}

