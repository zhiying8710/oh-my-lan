package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/zhiying8710/oh-my-lan/internal/logging"
	"github.com/zhiying8710/oh-my-lan/internal/proto"
)

// TestAdminLogs_EmptyBufferReturnsEmptyArray
// 没注入 logBuf（CLI-only / 测试场景常见）：handler 返回 200 + entries=[]。
// 关键：entries 必须是 []，不能是 null——前端 .map() 会爆。
func TestAdminLogs_EmptyBufferReturnsEmptyArray(t *testing.T) {
	ts, srv := newTestServer(t)
	bearer := newAdminBearer(t, srv)
	raw := doRaw(t, ts, http.MethodGet, "/api/admin/logs", bearer, "", http.StatusOK)

	// 直接验证 JSON 形状：entries 必须是 array
	var got struct {
		Entries []proto.LogEntryDTO `json:"entries"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if got.Entries == nil {
		t.Errorf("entries 应为 []，非 null：raw=%s", raw)
	}
	if len(got.Entries) != 0 {
		t.Errorf("无 logBuf 应返回 0 条，got %d", len(got.Entries))
	}
}

// TestAdminLogs_WithBuffer_ReturnsRecentEntries
// 注入一个真实 RingBuffer + bufferHandler，写几条日志，验证 handler 反查能拿到。
func TestAdminLogs_WithBuffer_ReturnsRecentEntries(t *testing.T) {
	ts, srv := newTestServer(t)
	bearer := newAdminBearer(t, srv)

	// 装一个 ring buffer 进 server；handler 直接读它
	buf := logging.NewRingBuffer(10)
	srv.logBuf = buf
	// 复用同一份 buffer 当 sink，logger 写出的 record 会进 buf
	srv.logger = slog.New(logging.NewBufferHandler(buf, slog.LevelDebug))

	srv.logger.Info("第一条", "k", "v")
	srv.logger.Warn("第二条")
	srv.logger.Error("第三条", "err", "boom")

	out := mustDoJSON[proto.LogsResponse](t, ts, http.MethodGet, "/api/admin/logs", bearer, "", http.StatusOK)
	if len(out.Entries) != 3 {
		t.Fatalf("expect 3 entries, got %d", len(out.Entries))
	}
	if out.Entries[0].Message != "第一条" {
		t.Errorf("顺序：第 0 条应是'第一条', got %q", out.Entries[0].Message)
	}
	if !strings.Contains(out.Entries[0].Attrs, "k=v") {
		t.Errorf("attrs 应含 k=v, got %q", out.Entries[0].Attrs)
	}
	if out.Entries[1].Level != "WARN" {
		t.Errorf("level 应为 WARN, got %q", out.Entries[1].Level)
	}
	if !strings.Contains(out.Entries[2].Attrs, "err=boom") {
		t.Errorf("ERROR attrs: got %q", out.Entries[2].Attrs)
	}
}

// TestAdminLogs_LimitClamping
// limit 参数：合法值取最近 N 条；负数/非数字回退到默认 200；>1000 钳到 1000。
func TestAdminLogs_LimitClamping(t *testing.T) {
	ts, srv := newTestServer(t)
	bearer := newAdminBearer(t, srv)

	buf := logging.NewRingBuffer(100)
	srv.logBuf = buf
	srv.logger = slog.New(logging.NewBufferHandler(buf, slog.LevelDebug))

	// 写 50 条
	for i := 0; i < 50; i++ {
		srv.logger.Info("m")
	}

	// limit=10 → 取最后 10 条
	r := mustDoJSON[proto.LogsResponse](t, ts, http.MethodGet, "/api/admin/logs?limit=10", bearer, "", http.StatusOK)
	if len(r.Entries) != 10 {
		t.Errorf("limit=10: got %d", len(r.Entries))
	}

	// limit=非数字 → fallback 到默认（不报错）。50 条全返回。
	r2 := mustDoJSON[proto.LogsResponse](t, ts, http.MethodGet, "/api/admin/logs?limit=abc", bearer, "", http.StatusOK)
	if len(r2.Entries) != 50 {
		t.Errorf("limit=abc 应回退到默认, got %d", len(r2.Entries))
	}

	// limit=0 → fallback 到默认（n>0 才取）
	r3 := mustDoJSON[proto.LogsResponse](t, ts, http.MethodGet, "/api/admin/logs?limit=0", bearer, "", http.StatusOK)
	if len(r3.Entries) != 50 {
		t.Errorf("limit=0 应回退到默认, got %d", len(r3.Entries))
	}
}

// TestAdminLogs_RequiresAdminAuth 无 token 拒绝。
func TestAdminLogs_RequiresAdminAuth(t *testing.T) {
	ts, _ := newTestServer(t)
	doRaw(t, ts, http.MethodGet, "/api/admin/logs", "", "", http.StatusUnauthorized)
}

// 防止 logging import 被 linter 当作"间接使用"误清。
var _ = io.Discard
