package server

import (
	"context"
	"fmt"
	"time"
)

// OfflineThreshold 是 device last_seen 超过该时长后被标 offline 的阈值。
const OfflineThreshold = 90 * time.Second

// reloadChiselUsers 把 store 里的全部设备同步进 chisel server 的 user index。
// 服务端重启后必须调用一次，让重连上来的 daemon 能通过认证。
// tunnel_secret 在 DB 中明文存储，按 SSH 私钥级别用文件权限保护。
func (s *Server) reloadChiselUsers(ctx context.Context) error {
	devices, err := s.store.ListDevices(ctx)
	if err != nil {
		return fmt.Errorf("列设备: %w", err)
	}
	for _, d := range devices {
		if err := s.tunnel.AddDevice(d.ID, d.TunnelSecret); err != nil {
			return fmt.Errorf("注入 chisel user %s: %w", d.ID, err)
		}
	}
	s.logger.Info("已恢复 chisel 用户", "count", len(devices))
	return nil
}

// runOfflineReaper 周期性地把心跳过期的设备标为 offline，并按需触发 bark 推送。
// 通过 ctx 控制生命周期；ctx 取消时干净退出。
func (s *Server) runOfflineReaper(ctx context.Context) {
	t := time.NewTicker(OfflineThreshold / 2)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		n, err := s.store.MarkStaleDevicesOffline(ctx, time.Now().Add(-OfflineThreshold))
		if err != nil {
			s.logger.Warn("offline reaper 失败", "err", err)
			continue
		}
		if n > 0 {
			s.logger.Info("标记 stale 设备为 offline", "count", n)
		}
		// bark push：对刚刚或仍然 offline 的设备发推送（已 alerted 的不重复发）
		s.maybePushBarkAlerts(ctx)
	}
}

// SessionReapInterval 是过期 session 清理周期。1 小时足够（session 默认 7 天）。
const SessionReapInterval = time.Hour

// SSHCleanupInterval / SSHGracePeriod 见 docs/security-via-ssh-tunnel.md。
//   - 每 6 小时扫一次 ssh_locked_at < now-7d 的 device 真删
//   - 7 天缓冲让 admin 误删后能"挽救"（手动恢复 authorized_keys + usermod -U）
const (
	SSHCleanupInterval = 6 * time.Hour
	SSHGracePeriod     = 7 * 24 * time.Hour
)

// runSSHAccountReaper 周期性真删过期账号 + DB row。
// 撤销 device 时立即 Lock + 标 ssh_locked_at；这里负责 grace period 后 userdel -r + DELETE FROM devices。
func (s *Server) runSSHAccountReaper(ctx context.Context) {
	t := time.NewTicker(SSHCleanupInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cutoff := time.Now().Add(-SSHGracePeriod)
		devices, err := s.store.ListSSHLockedBefore(ctx, cutoff)
		if err != nil {
			s.logger.Warn("ssh cleanup 列表失败", "err", err)
			continue
		}
		for _, d := range devices {
			if s.sshacct != nil {
				if err := s.sshacct.Delete(ctx, d.ID); err != nil {
					s.logger.Warn("ssh userdel 失败", "device", d.ID, "err", err)
					continue
				}
			}
			if err := s.store.DeleteDevice(ctx, d.ID); err != nil {
				s.logger.Warn("ssh cleanup 删 device 失败", "device", d.ID, "err", err)
				continue
			}
			s.logger.Info("ssh cleanup 完成", "device", d.ID, "user", d.SSHUsername)
		}
	}
}

// runSessionReaper 周期性清掉过期的登录 session。
func (s *Server) runSessionReaper(ctx context.Context) {
	// 启动时先跑一次，避免 server 长时间下线后第一次清理要等一小时
	if n, err := s.store.DeleteExpiredSessions(ctx, time.Now()); err != nil {
		s.logger.Warn("session reaper 初次失败", "err", err)
	} else if n > 0 {
		s.logger.Info("清理过期 session", "count", n)
	}
	t := time.NewTicker(SessionReapInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		n, err := s.store.DeleteExpiredSessions(ctx, time.Now())
		if err != nil {
			s.logger.Warn("session reaper 失败", "err", err)
			continue
		}
		if n > 0 {
			s.logger.Info("清理过期 session", "count", n)
		}
	}
}
