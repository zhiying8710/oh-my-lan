// Package server 装配 oh-my-lan 服务端的控制平面：HTTP API + chisel 隧道。
//
// 包内文件职责：
//   - server.go      ：Server struct + Options + New + Start/shutdown 主流程
//   - router.go      ：HTTP 路由注册
//   - lifecycle.go   ：chisel users 同步、离线 reaper
//   - helpers.go     ：响应工具、DTO mapper、中间件 helpers
//   - auth.go        ：device 与 admin 两套 bearer 认证
//   - handler_device.go：device 端 API handlers
//   - handler_admin.go ：admin 端 API handlers
//   - portalloc.go   ：端口池分配
//   - keyseed.go     ：chisel SSH key seed 持久化
//   - web.go         ：embed.FS 嵌入 Admin UI 静态资源
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/zhiying8710/oh-my-lan/internal/enroll"
	"github.com/zhiying8710/oh-my-lan/internal/logging"
	"github.com/zhiying8710/oh-my-lan/internal/server/api"
	"github.com/zhiying8710/oh-my-lan/internal/server/prober"
	"github.com/zhiying8710/oh-my-lan/internal/server/sshacct"
	"github.com/zhiying8710/oh-my-lan/internal/store"
	"github.com/zhiying8710/oh-my-lan/internal/tunnel"
)

// Options 是 Server 的构建参数。
type Options struct {
	ListenAddr          string // 控制平面 HTTP 监听
	ChiselListenAddr    string // chisel server 监听
	ChiselAdvertiseAddr string // 告诉客户端去连接的 chisel 地址（可能是公网域名:端口）
	ChiselKeySeed       string // 空则自动从 DataDir 持久化生成
	ChiselVerbose       bool
	DataDir             string
	PortMin, PortMax    int
	Store               *store.Store
	Logger              *slog.Logger
	LogBuffer           *logging.RingBuffer // 可选；非 nil 时 /api/admin/logs 暴露最近日志
	// SSH 跳板配置——见 docs/security-via-ssh-tunnel.md
	SSHHost           string          // VPS 公网 host，客户端 enroll 后用于 ssh -L 跳板。空时回退 ChiselAdvertiseAddr 的 host
	SSHPort           int             // VPS sshd 端口。默认 22
	SSHAcctDisabled   bool            // 测试场景关掉账号自动管理（测试场景没 sudo）
}

// Server 是控制平面 + 隧道的组合体。
type Server struct {
	logger              *slog.Logger
	store               *store.Store
	enroll              *enroll.Service
	tunnel              *tunnel.Server
	ports               *api.PortAllocator
	chiselAdvertiseAddr string
	startedAt           time.Time

	loginRL *api.LoginRateLimiter // 登录失败 5/min/IP，见 api/api.go
	logBuf  *logging.RingBuffer // C1: server 日志环形 buffer；可能为 nil（测试 / cli 入口未连）
	auditor *api.Auditor        // 包装 (store + logger) 写一条 audit 记录；s.audit 委托给它
	sshacct *sshacct.Manager    // VPS 受限 SSH 账号管理；nil 表示测试场景（不动 /etc/passwd）
	sshHost string              // 给 enroll 响应的 ssh host（公网 IP / 域名）
	sshPort int                 // 给 enroll 响应的 ssh port（默认 22）

	httpSrv *http.Server
}

func New(opts Options) (*Server, error) {
	if opts.Store == nil {
		return nil, errors.New("Store 必填")
	}
	if opts.Logger == nil {
		return nil, errors.New("Logger 必填")
	}

	advertise := opts.ChiselAdvertiseAddr
	if advertise == "" {
		advertise = opts.ChiselListenAddr
	}

	seed, err := loadOrCreateKeySeed(opts.DataDir, opts.ChiselKeySeed)
	if err != nil {
		return nil, err
	}

	tun, err := tunnel.NewServer(tunnel.ServerConfig{
		ListenAddr: opts.ChiselListenAddr,
		KeySeed:    seed,
		Verbose:    opts.ChiselVerbose,
	})
	if err != nil {
		return nil, fmt.Errorf("构建 tunnel server: %w", err)
	}

	// SSH 跳板 host：默认从 ChiselAdvertiseAddr 取（"vps.example.com:58443" → "vps.example.com"）。
	sshHost := opts.SSHHost
	if sshHost == "" {
		if h, _, err := net.SplitHostPort(advertise); err == nil {
			sshHost = h
		}
	}
	sshPort := opts.SSHPort
	if sshPort == 0 {
		sshPort = 22
	}
	var sshMgr *sshacct.Manager
	if !opts.SSHAcctDisabled {
		sshMgr = sshacct.New(opts.Logger)
	}

	return &Server{
		logger:              opts.Logger,
		store:               opts.Store,
		enroll:              enroll.New(opts.Store),
		tunnel:              tun,
		ports:               api.NewPortAllocator(opts.Store, opts.PortMin, opts.PortMax),
		chiselAdvertiseAddr: advertise,
		startedAt:           time.Now(),
		loginRL:             api.NewLoginRateLimiter(),
		logBuf:              opts.LogBuffer,
		auditor:             &api.Auditor{Store: opts.Store, Logger: opts.Logger},
		sshacct:             sshMgr,
		sshHost:             sshHost,
		sshPort:             sshPort,
		httpSrv: &http.Server{
			Addr:              opts.ListenAddr,
			ReadHeaderTimeout: 10 * time.Second,
		},
	}, nil
}

// Start 启动 chisel server 和控制 HTTP server。阻塞直到 ctx 取消或任一组件出错。
func (s *Server) Start(ctx context.Context) error {
	if err := s.reloadChiselUsers(ctx); err != nil {
		return err
	}

	mux := http.NewServeMux()
	s.registerRoutes(mux)
	// 给所有路径包一层 CORS（静态资源 /admin/ 用同源加载，但加上 CORS 也无害）
	s.httpSrv.Handler = api.WithCORS(mux)

	if err := s.tunnel.Start(ctx); err != nil {
		return fmt.Errorf("启动 chisel server: %w", err)
	}
	s.logger.Info("chisel server 已启动",
		"listen", s.httpSrv.Addr,
		"fingerprint", s.tunnel.Fingerprint(),
	)

	reaperCtx, cancelReaper := context.WithCancel(ctx)
	defer cancelReaper()
	go s.runOfflineReaper(reaperCtx)
	go s.runSessionReaper(reaperCtx)
	go s.runSSHAccountReaper(reaperCtx)
	go prober.New(s.store, s.logger).Run(reaperCtx)

	errCh := make(chan error, 2)
	go func() {
		if err := s.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("HTTP server 异常退出: %w", err)
		}
	}()
	go func() {
		if err := s.tunnel.Wait(); err != nil {
			errCh <- fmt.Errorf("chisel server: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		return s.shutdown()
	case err := <-errCh:
		_ = s.shutdown()
		return err
	}
}

func (s *Server) shutdown() error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	httpErr := s.httpSrv.Shutdown(shutdownCtx)
	tunnelErr := s.tunnel.Close()
	if httpErr != nil {
		return httpErr
	}
	return tunnelErr
}
