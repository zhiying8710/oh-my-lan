package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zhiying8710/oh-my-lan/internal/store"
)

func newAllocStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestPortAllocator_PicksLowestAvailable(t *testing.T) {
	ctx := context.Background()
	s := newAllocStore(t)
	if err := s.CreateDevice(ctx, store.Device{ID: "d", Name: "n", TunnelSecret: "x", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	alloc := NewPortAllocator(s, 40000, 40002)

	for want := 40000; want <= 40002; want++ {
		got, err := alloc.Allocate(ctx)
		if err != nil {
			t.Fatalf("Allocate: %v", err)
		}
		if got != want {
			t.Errorf("Allocate=%d want %d", got, want)
		}
		// 把分配出来的端口落库以让下一轮跳过
		if err := s.CreateService(ctx, store.Service{
			ID:         fakeID(t),
			DeviceID:   "d",
			Name:       fakeID(t),
			Protocol:   "tcp",
			LocalAddr:  "127.0.0.1:1",
			PublicPort: got,
			Enabled:    true,
			CreatedAt:  time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	_, err := alloc.Allocate(ctx)
	if !errors.Is(err, ErrPortPoolExhausted) {
		t.Errorf("用尽后应 ErrPortPoolExhausted, got %v", err)
	}
}

func fakeID(t *testing.T) string {
	t.Helper()
	return "id-" + time.Now().Format("150405.000000000")
}
