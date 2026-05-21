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
	"net/http"
	"time"

	"github.com/zhiying8710/oh-my-lan/internal/enroll"
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
}

// Server 是控制平面 + 隧道的组合体。
type Server struct {
	logger              *slog.Logger
	store               *store.Store
	enroll              *enroll.Service
	tunnel              *tunnel.Server
	ports               *PortAllocator
	chiselAdvertiseAddr string
	startedAt           time.Time

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

	return &Server{
		logger:              opts.Logger,
		store:               opts.Store,
		enroll:              enroll.New(opts.Store),
		tunnel:              tun,
		ports:               NewPortAllocator(opts.Store, opts.PortMin, opts.PortMax),
		chiselAdvertiseAddr: advertise,
		startedAt:           time.Now(),
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
	s.httpSrv.Handler = withCORS(mux)

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

	errCh := make(chan error, 2)
	go func() {
		if err := s.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("HTTP server: %w", err)
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
