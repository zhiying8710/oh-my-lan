// Package prober 实现 A1' 链路健康探测：周期性 TCP-dial 每个 enabled service 的
// public_port，把结果写回 store（last_probe_at + last_probe_ok）。
//
// 拆出独立包的动机：原先是 internal/server 包内 33 文件之一，与 admin/device handler
// 同居 godpackage。逻辑上 prober 是后台任务，与 HTTP 路由完全无关，唯一依赖是 store +
// logger，是最适合先抽出来的"vertical slice"。其它子包（auth/device/admin）跟着这个模式走。
package prober

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/zhiying8710/oh-my-lan/internal/store"
)

// Interval 是链路探测周期。45s 让一分钟内能有一次刷新，
// 与 offline reaper 周期对齐避免和 list rebuild 抢锁。
const Interval = 45 * time.Second

// Timeout 是单次 TCP dial 的最长等待。3 秒——
// 公网到 VPS 跨洋链路也够；再长拖累整体探测周期。
const Timeout = 3 * time.Second

// Store 是 prober 需要的最小 store 子集。把它声明成接口而非直接 import *store.Store，
// 让测试可以注入 fake，也让"prober 真正需要 store 的哪些能力"显式可见。
type Store interface {
	ListServicesJoined(ctx context.Context, deviceID string) ([]store.ServiceListItem, error)
	RecordServiceProbe(ctx context.Context, serviceID string, ok bool, ts time.Time) error
}

// Prober 持有 store 与 logger，可被 server orchestrator 启动 / 停止。
type Prober struct {
	store  Store
	logger *slog.Logger
}

// New 构造 Prober。logger 不能为 nil——上层应注入 slog.New(NoopHandler) 而非传 nil。
func New(s Store, logger *slog.Logger) *Prober {
	return &Prober{store: s, logger: logger}
}

// Run 周期性 TCP-dial 每个 enabled service 的 public_port，写 last_probe_at + last_probe_ok 到 services 表。
// UI 用这个判断"链路是否真通"（chisel 控制面心跳活着 != 公网端口可达）。
//
// 局限：
//   - 只测 TCP；UDP service 我们仍然 dial 它的 TCP port，会失败但 ok=false 是预期
//     ——UDP forward 是否能用还是要靠业务层探活，这里诚实承认 false 而不是假装成功。
//     未来可按 protocol 分流。
//   - 不区分"公网端口未 listen"与"listen 但不通"——dial 失败都视作 not OK，
//     这是 admin 视角的简化（无论哪种坏都需要排查）。
//   - 并发上限：每周期所有 service 并发 dial，单台 server 几十个 service 完全扛得住；
//     真到上百时再加 worker pool。
func (p *Prober) Run(ctx context.Context) {
	t := time.NewTicker(Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		p.ProbeAll(ctx)
	}
}

// ProbeAll 跑一轮完整探测。一般由 Run 周期调用；测试场景下可以直接调一次再断言。
func (p *Prober) ProbeAll(ctx context.Context) {
	list, err := p.store.ListServicesJoined(ctx, "")
	if err != nil {
		p.logger.Warn("health prober 列服务失败", "err", err)
		return
	}
	var wg sync.WaitGroup
	for _, it := range list {
		if !it.Enabled {
			continue
		}
		wg.Add(1)
		go func(serviceID string, port int) {
			defer wg.Done()
			ok := ProbeTCPPort(ctx, port)
			if err := p.store.RecordServiceProbe(ctx, serviceID, ok, time.Now()); err != nil {
				// ctx 已取消时 store 也会返回 err；不打日志免得 shutdown 噪音
				if ctx.Err() == nil {
					p.logger.Warn("写 service probe 失败", "service", serviceID, "err", err)
				}
			}
		}(it.ID, it.PublicPort)
	}
	wg.Wait()
}

// ProbeTCPPort 在本机 dial 127.0.0.1:<port>（chisel 把 R: 公网端口 listen 在本机）。
// 注意：不 dial server 自己的公网域名——避免 NAT 回环 / DNS 解析失败把全部 service 误判 down。
// chisel server 的 listener 一定在 0.0.0.0:port，127.0.0.1 一样能到。
//
// ctx-aware：server shutdown 时 reaper ctx cancel，正在 dial 的 goroutine 立刻收 ctx 取消，
// 不会等满 3 秒 timeout。多设备情况下能把 shutdown 拖延上限压到秒级而不是 (N×3s)。
func ProbeTCPPort(ctx context.Context, port int) bool {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	d := net.Dialer{Timeout: Timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
