package logging

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestRingBuffer_BelowCapacity(t *testing.T) {
	buf := NewRingBuffer(5)
	for i := 0; i < 3; i++ {
		buf.Add(LogEntry{Message: "m" + string(rune('0'+i))})
	}
	got := buf.Snapshot(10)
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
	if got[0].Message != "m0" || got[2].Message != "m2" {
		t.Fatalf("order wrong: %v", got)
	}
}

func TestRingBuffer_Wraps(t *testing.T) {
	buf := NewRingBuffer(3)
	for i := 0; i < 7; i++ {
		buf.Add(LogEntry{Message: "m" + string(rune('0'+i))})
	}
	// 写了 7 条，cap 3 → 应该剩最后 3 条 m4 m5 m6
	got := buf.Snapshot(10)
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
	want := []string{"m4", "m5", "m6"}
	for i, w := range want {
		if got[i].Message != w {
			t.Fatalf("at %d want %q, got %q", i, w, got[i].Message)
		}
	}
}

func TestRingBuffer_LimitClampsToContent(t *testing.T) {
	buf := NewRingBuffer(10)
	for i := 0; i < 5; i++ {
		buf.Add(LogEntry{Message: "m"})
	}
	// limit > size → 返回全部 5 条；limit < size → 返回最后 limit 条
	if got := buf.Snapshot(100); len(got) != 5 {
		t.Fatalf("limit>size: want 5, got %d", len(got))
	}
	if got := buf.Snapshot(2); len(got) != 2 {
		t.Fatalf("limit=2: want 2, got %d", len(got))
	}
}

func TestBufferHandler_CapturesAttrs(t *testing.T) {
	buf := NewRingBuffer(10)
	h := NewBufferHandler(buf, slog.LevelDebug)
	logger := slog.New(h)
	logger.Info("hello", "device", "A", "port", 1234)
	snap := buf.Snapshot(0)
	if len(snap) != 1 {
		t.Fatalf("want 1 entry, got %d", len(snap))
	}
	e := snap[0]
	if e.Message != "hello" {
		t.Fatalf("msg: %q", e.Message)
	}
	if e.Level != "INFO" {
		t.Fatalf("level: %q", e.Level)
	}
	if !strings.Contains(e.Attrs, "device=A") || !strings.Contains(e.Attrs, "port=1234") {
		t.Fatalf("attrs missing: %q", e.Attrs)
	}
	if time.Since(e.Time) > time.Second {
		t.Fatalf("time too old: %v", e.Time)
	}
}
