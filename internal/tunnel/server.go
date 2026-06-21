package tunnel

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"

	chserver "github.com/jpillora/chisel/server"
)

// ServerConfig 是 Server 的构建参数。
type ServerConfig struct {
	ListenAddr string // 形如 ":8443"
	KeySeed    string // 可选；空则随机生成
	Verbose    bool   // 开 chisel server 的 Debug 日志
}

// Server 包装 chisel server，提供 Start/Close 和动态用户管理。
//
// devices 是 chisel UserIndex 的并行可见性 mirror——chisel 自家 UserIndex 不暴露
// Has/Get 接口，我们要支持 "kick 完后查 phantom user 残留" 这类断言（admin race 测试）
// 和 future admin "查活跃 chisel session" UI，必须自己维持一份集合。
type Server struct {
	cfg     ServerConfig
	srv     *chserver.Server
	mu      sync.Mutex
	devices map[string]struct{}
}

func NewServer(cfg ServerConfig) (*Server, error) {
	chCfg := &chserver.Config{
		KeySeed: cfg.KeySeed,
		Reverse: true, // 必须开启，否则客户端 R: spec 会被拒绝
	}
	srv, err := chserver.NewServer(chCfg)
	if err != nil {
		return nil, fmt.Errorf("构建 chisel server: %w", err)
	}
	if cfg.Verbose {
		srv.Debug = true
	}
	return &Server{cfg: cfg, srv: srv, devices: map[string]struct{}{}}, nil
}

// Start 在后台启动 chisel server，监听 cfg.ListenAddr。
// 通过 ctx 取消可优雅停止。
func (s *Server) Start(ctx context.Context) error {
	host, port, err := splitHostPort(s.cfg.ListenAddr)
	if err != nil {
		return err
	}
	return s.srv.StartContext(ctx, host, port)
}

// Wait 阻塞直到 chisel server 退出。
func (s *Server) Wait() error { return s.srv.Wait() }

// Close 强制关闭。
func (s *Server) Close() error { return s.srv.Close() }

// Fingerprint 是 chisel server 的 SSH 公钥指纹，客户端用它做 host key 校验。
func (s *Server) Fingerprint() string { return s.srv.GetFingerprint() }

// AddDevice 动态注册一个设备身份；password 是 tunnel_secret 明文（chisel 内部仍是内存里）。
// addrs 控制该 device 允许 R: spec 的地址正则；个人使用默认放开。
func (s *Server) AddDevice(deviceID, secret string) error {
	// chisel 的 ACL 是正则，".+" 表示任意。
	if err := s.srv.AddUser(deviceID, secret, ".+"); err != nil {
		return err
	}
	s.mu.Lock()
	s.devices[deviceID] = struct{}{}
	s.mu.Unlock()
	return nil
}

// RemoveDevice 撤销设备身份。
func (s *Server) RemoveDevice(deviceID string) {
	s.srv.DeleteUser(deviceID)
	s.mu.Lock()
	delete(s.devices, deviceID)
	s.mu.Unlock()
}

// HasDevice 报告 device 是否在 chisel UserIndex（mirror 视图）。
// 用于断言 / admin "活跃 chisel 用户" 列表；不能用来代替 chisel 内部的鉴权检查。
func (s *Server) HasDevice(deviceID string) bool {
	s.mu.Lock()
	_, ok := s.devices[deviceID]
	s.mu.Unlock()
	return ok
}

func splitHostPort(addr string) (string, string, error) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", "", fmt.Errorf("listen 地址非法 %q: %w", addr, err)
	}
	if _, err := strconv.Atoi(p); err != nil {
		return "", "", fmt.Errorf("端口非数字 %q", p)
	}
	if h == "" {
		h = "0.0.0.0"
	}
	return h, p, nil
}
