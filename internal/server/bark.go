package server

// bark.go: 只保留 server-side 的 offline reaper 调用——maybePushBarkAlerts。
// 三个 admin handler (Get/Put/Test) 已迁到 internal/server/admin/bark.go。
// 共享工具 SendBarkPush + FormatRelativeTime 在 internal/server/api/。

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/zhiying8710/oh-my-lan/internal/server/api"
	"github.com/zhiying8710/oh-my-lan/internal/store"
)

// maybePushBarkAlerts 检查 bark 是否启用、设备是否离线、是否已推过。
//
// 设计上不阻塞 offline reaper：bark URL 一旦不可达，每个 push 会 10s 超时；
// 5 台离线设备 → 串行 50s，超过 reaper 周期 (45s)，把后续 offline 检测拖到下一个 cycle 之外。
// 因此每台设备的 push 起一个 goroutine，WaitGroup 在函数返回前收尾（最多额外 1 个周期），
// 把"bark 不可达"局限在 push 子系统内，offline 状态机不受影响。
func (s *Server) maybePushBarkAlerts(ctx context.Context) {
	bs, err := s.store.GetBarkSettings(ctx)
	if err != nil || !bs.Enabled || bs.BarkURL == "" {
		return
	}
	devices, err := s.store.ListDevices(ctx)
	if err != nil {
		return
	}
	threshold := time.Duration(bs.OfflineThresholdSeconds) * time.Second
	now := time.Now()
	var wg sync.WaitGroup
	for _, d := range devices {
		// LastSeenAt 是 *time.Time——从未上报心跳为 nil
		var lastSeen time.Time
		if d.LastSeenAt != nil {
			lastSeen = *d.LastSeenAt
		}
		offline := lastSeen.IsZero() || now.Sub(lastSeen) > threshold
		alerted, _ := s.store.IsDeviceAlerted(ctx, d.ID)
		if !offline {
			// 回到 online，清告警状态，下次掉线会重新推
			if alerted {
				_ = s.store.ClearDeviceAlert(ctx, d.ID)
			}
			continue
		}
		if alerted {
			continue // 已经推过这次离线，不重复打扰
		}
		// 并发推；每个 goroutine 自己控制 timeout（api.SendBarkPush 内置 10s），
		// reaper 主 goroutine 在 WaitGroup 收尾后退出，不会被单台 bark URL 故障拖死
		wg.Add(1)
		go func(d store.Device, lastSeen time.Time, barkURL string) {
			defer wg.Done()
			title := "oh-my-lan · 设备离线"
			body := fmt.Sprintf("%s 已掉线（最后活跃 %s）",
				d.Name, api.FormatRelativeTime(lastSeen))
			if err := api.SendBarkPush(ctx, barkURL, title, body); err != nil {
				s.logger.Warn("bark 推送失败", "device", d.Name, "err", err)
				return // 不 MarkAlerted，下次 tick 重试
			}
			if err := s.store.MarkDeviceAlerted(ctx, d.ID); err != nil {
				s.logger.Warn("登记 device_alert_state 失败", "device", d.ID, "err", err)
			}
			s.logger.Info("bark 已推送设备离线告警", "device", d.Name)
		}(d, lastSeen, bs.BarkURL)
	}
	wg.Wait()
}
