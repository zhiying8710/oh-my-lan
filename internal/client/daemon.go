package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/zhiying8710/oh-my-lan/internal/proto"
	"github.com/zhiying8710/oh-my-lan/internal/tunnel"
)

// DefaultReloadInterval 是 daemon 轮询 bootstrap 的默认间隔。
const DefaultReloadInterval = 30 * time.Second

// Daemon 是客户端常驻进程的主结构。
type Daemon struct {
	state          *State
	api            *APIClient
	logger         *slog.Logger
	verbose        bool
	reloadInterval time.Duration
}

func NewDaemon(state *State, logger *slog.Logger) *Daemon {
	api := NewAPIClient(state.ServerURL)
	api.DeviceID = state.DeviceID
	api.Secret = state.TunnelSecret
	return &Daemon{
		state:          state,
		api:            api,
		logger:         logger,
		reloadInterval: DefaultReloadInterval,
	}
}

// SetVerbose 控制底层 chisel client 是否打印调试日志。
func (d *Daemon) SetVerbose(v bool) { d.verbose = v }

// SetReloadInterval 覆盖默认轮询间隔；< 1s 视为 1s。
func (d *Daemon) SetReloadInterval(t time.Duration) {
	if t < time.Second {
		t = time.Second
	}
	d.reloadInterval = t
}

// Run 阻塞执行 daemon 主循环：拉 bootstrap → 启 chisel client → 配置变更或断线后重启。
// 若服务端返回 401（device 被 admin 撤销），返回 client.ErrUnauthorized 让上层退出。
func (d *Daemon) Run(ctx context.Context) error {
	backoff := time.Second
	const maxBackoff = 60 * time.Second

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		err := d.runOnce(ctx)
		switch {
		case errors.Is(err, context.Canceled):
			return err
		case errors.Is(err, ErrUnauthorized):
			d.logger.Error("device 已被服务端撤销或凭证失效，daemon 退出", "err", err)
			return err
		case errors.Is(err, errReloadRequested):
			// 配置变更触发的主动重启，立刻重试且不退避
			backoff = time.Second
			continue
		case err != nil:
			d.logger.Warn("daemon 连接异常，准备重连", "err", err, "next_in", backoff.String())
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		default:
			backoff = time.Second
		}
	}
}

// errReloadRequested 是内部哨兵，表示因配置变更主动断开。
var errReloadRequested = errors.New("reload requested")

// stopReason 表示 runOnce 内部"为何主动结束"。
type stopReason int

const (
	stopReasonReload       stopReason = iota // 配置变更，外层应立刻重连
	stopReasonUnauthorized                   // 服务端 401，外层应终止 daemon
)

func (d *Daemon) runOnce(ctx context.Context) error {
	boot, err := d.api.Bootstrap(ctx)
	if err != nil {
		if errors.Is(err, ErrUnauthorized) {
			return err
		}
		return fmt.Errorf("拉 bootstrap: %w", err)
	}
	remotes, locals := bootToSpecs(boot)
	baseline := hashSpecs(remotes, locals)

	d.logger.Info("bootstrap 完成",
		"chisel_addr", boot.ChiselAddr,
		"fingerprint", boot.ServerFingerprint,
		"remotes", len(remotes),
		"locals", len(locals),
	)

	cli, err := tunnel.NewClient(tunnel.ClientConfig{
		ServerURL:   normalizeChiselURL(boot.ChiselAddr),
		Fingerprint: boot.ServerFingerprint,
		DeviceID:    d.state.DeviceID,
		Secret:      d.state.TunnelSecret,
		Remotes:     remotes,
		Locals:      locals,
		Verbose:     d.verbose,
	})
	if err != nil {
		return fmt.Errorf("构建 chisel client: %w", err)
	}

	// runCtx 控制 chisel client；reloadCtx 控制轮询 goroutine
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	runErrCh := make(chan error, 1)
	go func() { runErrCh <- cli.Run(runCtx) }()

	stopCh := make(chan stopReason, 1)
	go d.watchReload(runCtx, baseline, stopCh)

	select {
	case err := <-runErrCh:
		return err
	case reason := <-stopCh:
		_ = cli.Close()
		cancelRun()
		<-runErrCh
		switch reason {
		case stopReasonUnauthorized:
			return ErrUnauthorized
		default:
			d.logger.Info("远端服务配置变更，重启隧道")
			return errReloadRequested
		}
	case <-ctx.Done():
		_ = cli.Close()
		cancelRun()
		<-runErrCh
		return ctx.Err()
	}
}

// watchReload 定期拉 bootstrap：
//   - 发现 remotes/locals hash 变更 → 通过 stopCh 通知主循环重启隧道
//   - 收到 401 → 通过 stopCh 通知主循环终止 daemon（device 被撤销）
//
// 同时充当 keepalive，让服务端感知设备在线。
func (d *Daemon) watchReload(ctx context.Context, baseline string, stopCh chan<- stopReason) {
	t := time.NewTicker(d.reloadInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		boot, err := d.api.Bootstrap(ctx)
		if err != nil {
			if errors.Is(err, ErrUnauthorized) {
				d.logger.Error("bootstrap 返回 401，device 已被撤销")
				select {
				case stopCh <- stopReasonUnauthorized:
				default:
				}
				return
			}
			if !errors.Is(err, context.Canceled) {
				d.logger.Warn("reload poll 失败", "err", err)
			}
			continue
		}
		r, l := bootToSpecs(boot)
		h := hashSpecs(r, l)
		if h != baseline {
			d.logger.Info("检测到 remotes 变更",
				"old_hash", baseline[:12]+"...",
				"new_hash", h[:12]+"...")
			select {
			case stopCh <- stopReasonReload:
			default:
			}
			return
		}
	}
}

func bootToSpecs(boot proto.BootstrapResponse) ([]tunnel.Remote, []tunnel.Local) {
	remotes := make([]tunnel.Remote, 0, len(boot.Remotes))
	for _, r := range boot.Remotes {
		remotes = append(remotes, tunnel.Remote{
			PublicPort: r.PublicPort,
			LocalAddr:  r.LocalAddr,
			Protocol:   r.Protocol,
			BindLocal:  r.BindLocal,
		})
	}
	locals := make([]tunnel.Local, 0, len(boot.Locals))
	for _, l := range boot.Locals {
		locals = append(locals, tunnel.Local{
			LocalPort:        l.LocalPort,
			RemotePublicPort: l.RemotePublicPort,
			Protocol:         l.Protocol,
		})
	}
	return remotes, locals
}

// hashSpecs 计算 (remotes, locals) 的稳定哈希，用于检测变更。
// remotes 和 locals 分别排序后串接，避免列表元素顺序波动导致 false reload。
func hashSpecs(rs []tunnel.Remote, ls []tunnel.Local) string {
	rKeys := make([]string, len(rs))
	for i, r := range rs {
		rKeys[i] = fmt.Sprintf("R|%d|%s|%s", r.PublicPort, r.Protocol, r.LocalAddr)
	}
	sort.Strings(rKeys)
	lKeys := make([]string, len(ls))
	for i, l := range ls {
		lKeys[i] = fmt.Sprintf("L|%d|%d|%s", l.LocalPort, l.RemotePublicPort, l.Protocol)
	}
	sort.Strings(lKeys)
	joined := strings.Join(rKeys, "\n") + "\n--\n" + strings.Join(lKeys, "\n")
	h := sha256.Sum256([]byte(joined))
	return hex.EncodeToString(h[:])
}

// normalizeChiselURL 把 "vps:8443" 补全成 chisel.Client 需要的 URL 格式。
func normalizeChiselURL(addr string) string {
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return addr
	}
	return "http://" + addr
}

// EnrollNew 是 enroll 流程的一次性 helper。
//
// 流程：
//  1. 客户端调 EnsureSSHKey 拿到本地 ed25519 公钥
//  2. POST /api/devices/enroll 带 token + name + pubkey
//  3. server 在 VPS 上建受限账号 + 写 authorized_keys + 返回 ssh_username/host/port
//  4. 客户端把 ssh 信息 + chisel 信息一起落 state.json
//
// 注意 sshPubkey 必须是 EnsureSSHKey 返回的"oml-managed"那份，与用户 ~/.ssh/* 隔离。
func EnrollNew(ctx context.Context, serverURL, token, deviceName, sshPubkey, statePath string) (*State, error) {
	api := NewAPIClient(serverURL)
	resp, err := api.Enroll(ctx, token, deviceName, sshPubkey)
	if err != nil {
		return nil, err
	}
	s := &State{
		ServerURL:         serverURL,
		DeviceID:          resp.DeviceID,
		DeviceName:        resp.DeviceName,
		TunnelSecret:      resp.TunnelSecret,
		ServerFingerprint: resp.ServerFingerprint,
		ChiselAddr:        resp.ChiselAddr,
		SSHUsername:       resp.SSHUsername,
		SSHHost:           resp.SSHHost,
		SSHPort:           resp.SSHPort,
	}
	if err := SaveState(statePath, s); err != nil {
		return nil, err
	}
	return s, nil
}

// EnrolledAPIClient 根据 state 构建已认证的 APIClient，用于 CLI 子命令。
func EnrolledAPIClient(state *State) *APIClient {
	api := NewAPIClient(state.ServerURL)
	api.DeviceID = state.DeviceID
	api.Secret = state.TunnelSecret
	return api
}
