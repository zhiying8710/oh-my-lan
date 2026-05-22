package prober

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/zhiying8710/oh-my-lan/internal/store"
)

// newStore 直接开 :memory: SQLite。子包测试不依赖 server 包的 newTestServer——
// prober 只用 store，把它独立测在拆包前后行为完全一致。
func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func newLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestProbeTCPPort_Listening 在本机随机端口起 listener，ProbeTCPPort 应该返回 true。
func TestProbeTCPPort_Listening(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	if ok := ProbeTCPPort(context.Background(), port); !ok {
		t.Errorf("listening 端口应 probe=true, port=%d", port)
	}
}

// TestProbeTCPPort_Closed 选一个临时绑过又释放的端口，probe 应返回 false（connection refused）。
func TestProbeTCPPort_Closed(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	time.Sleep(10 * time.Millisecond)

	if ok := ProbeTCPPort(context.Background(), port); ok {
		t.Errorf("已关闭端口不应 probe=true, port=%d", port)
	}
}

// TestProbeTCPPort_CtxCanceled 验证 ctx-aware dial：server shutdown 时 ctx cancel，
// ProbeTCPPort 应迅速返回 false 而非等满 Timeout (3s)。
//
// 直接用 "RFC5737 黑洞 IP + 时序 cancel" 在 macOS / Docker / 企业网络下不可靠：
//   - 透明代理会把所有 outbound TCP 接住 → dial 成功，破坏前提
//   - SYN 被本地防火墙立即 reject → err 来自 reject 而非 cancel
// 改用"先 cancel 再 dial"：DialContext 必须立刻检测到 ctx 已死并放弃。
func TestProbeTCPPort_CtxCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 提前 cancel

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	start := time.Now()
	got := ProbeTCPPort(ctx, port)
	elapsed := time.Since(start)
	if got {
		t.Fatal("已 cancel 的 ctx 不应 probe=true")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("ctx canceled 后 probe 花了 %v（应 <500ms）", elapsed)
	}
}

// TestProbeAll_RecordsResults 起两个真实的本机 listener，跑一次 ProbeAll，
// 再读 ListServicesJoined 验证写入。覆盖 store.RecordServiceProbe 写入和后续读出。
func TestProbeAll_RecordsResults(t *testing.T) {
	st := newStore(t)
	p := New(st, newLogger())
	ctx := t.Context()

	lnA, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lnA.Close()
	lnB, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lnB.Close()
	portA := lnA.Addr().(*net.TCPAddr).Port
	portB := lnB.Addr().(*net.TCPAddr).Port

	now := time.Now().UTC()
	if err := st.CreateDevice(ctx, store.Device{
		ID: "dev-prober", Name: "prober-dev", TunnelSecret: "hash", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	mkSvc := func(id string, port int, enabled bool) {
		t.Helper()
		if err := st.CreateService(ctx, store.Service{
			ID: id, DeviceID: "dev-prober", Name: id, Protocol: "tcp",
			LocalAddr: "127.0.0.1:1", PublicPort: port, Enabled: enabled, CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	mkSvc("svc-a", portA, true)
	mkSvc("svc-b", portB, true)
	// 第三个：disabled，不应被探测。
	mkSvc("svc-c", 1, false)

	p.ProbeAll(ctx)

	list, err := st.ListServicesJoined(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]struct {
		probeAt *time.Time
		probeOK bool
	}{}
	for _, it := range list {
		byID[it.ID] = struct {
			probeAt *time.Time
			probeOK bool
		}{it.LastProbeAt, it.LastProbeOK}
	}

	if a := byID["svc-a"]; a.probeAt == nil || !a.probeOK {
		t.Errorf("svc-a 应有成功 probe 记录, got %+v", a)
	}
	if b := byID["svc-b"]; b.probeAt == nil || !b.probeOK {
		t.Errorf("svc-b 应有成功 probe 记录, got %+v", b)
	}
	if c := byID["svc-c"]; c.probeAt != nil {
		t.Errorf("disabled svc-c 不应被探测, got %+v", c)
	}
}

// TestProbeAll_RecordsFailure 关掉 listener 再探，应记录 last_probe_ok=false。
func TestProbeAll_RecordsFailure(t *testing.T) {
	st := newStore(t)
	p := New(st, newLogger())
	ctx := t.Context()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	now := time.Now().UTC()
	if err := st.CreateDevice(ctx, store.Device{
		ID: "dev-fail", Name: "fail-dev", TunnelSecret: "hash", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateService(ctx, store.Service{
		ID: "svc-fail", DeviceID: "dev-fail", Name: "x", Protocol: "tcp",
		LocalAddr: "127.0.0.1:1", PublicPort: port, Enabled: true, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	p.ProbeAll(ctx)

	list, err := st.ListServicesJoined(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, it := range list {
		if it.ID != "svc-fail" {
			continue
		}
		found = true
		if it.LastProbeAt == nil {
			t.Errorf("应记录探测时间")
		}
		if it.LastProbeOK {
			t.Errorf("端口已关，应 probe=false")
		}
	}
	if !found {
		t.Errorf("svc-fail 未在 list 中找到")
	}
}
