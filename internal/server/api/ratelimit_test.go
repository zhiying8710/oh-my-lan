package api

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiter_AllowUntilThreshold(t *testing.T) {
	rl := NewLoginRateLimiter()
	ip := "192.0.2.1"
	for i := 0; i < rl.maxFail; i++ {
		if !rl.Allow(ip) {
			t.Fatalf("第 %d 次应该允许 (maxFail=%d)", i+1, rl.maxFail)
		}
		rl.RecordFailure(ip)
	}
	if rl.Allow(ip) {
		t.Fatal("超过阈值应该拒绝")
	}
}

func TestRateLimiter_PerIPIsolation(t *testing.T) {
	rl := NewLoginRateLimiter()
	a, b := "10.0.0.1", "10.0.0.2"
	for i := 0; i < rl.maxFail; i++ {
		rl.RecordFailure(a)
	}
	if rl.Allow(a) {
		t.Fatal("A 超过阈值")
	}
	if !rl.Allow(b) {
		t.Fatal("B 不该受 A 影响")
	}
}

func TestRateLimiter_WindowSliding(t *testing.T) {
	rl := NewLoginRateLimiter()
	rl.window = 50 * time.Millisecond
	ip := "10.0.0.3"
	for i := 0; i < rl.maxFail; i++ {
		rl.RecordFailure(ip)
	}
	if rl.Allow(ip) {
		t.Fatal("先应拒")
	}
	time.Sleep(70 * time.Millisecond) // 窗口外
	if !rl.Allow(ip) {
		t.Fatal("窗口滑出后应恢复")
	}
}

func TestRateLimiter_GcDeletesEmptyEntries(t *testing.T) {
	rl := NewLoginRateLimiter()
	rl.window = 30 * time.Millisecond
	ip := "10.0.0.4"
	rl.RecordFailure(ip)
	time.Sleep(50 * time.Millisecond)
	_ = rl.Allow(ip) // 触发 gc
	rl.mu.Lock()
	_, exists := rl.failures[ip]
	rl.mu.Unlock()
	if exists {
		t.Fatal("窗口过期后应从 map 删 entry，避免长跑内存膨胀")
	}
}

func TestRemoteIP_StripsPort(t *testing.T) {
	r := httptest.NewRequest("POST", "/", nil)
	r.RemoteAddr = "203.0.113.5:54321"
	if got := RemoteIP(r); got != "203.0.113.5" {
		t.Fatalf("want 203.0.113.5, got %q", got)
	}
}

func TestRemoteIP_FallbackOnMalformedAddr(t *testing.T) {
	r := httptest.NewRequest("POST", "/", nil)
	r.RemoteAddr = "no-port-here"
	if got := RemoteIP(r); got != "no-port-here" {
		t.Fatalf("解析失败应原样返回，得 %q", got)
	}
}
