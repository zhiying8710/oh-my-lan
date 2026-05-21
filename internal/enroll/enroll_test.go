package enroll

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zhiying8710/oh-my-lan/internal/store"
)

func newSvc(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	s, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return New(s), s
}

func TestIssueAndEnroll_HappyPath(t *testing.T) {
	ctx := context.Background()
	svc, _ := newSvc(t)

	tok, err := svc.IssueToken(ctx, 0)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	if tok.Token == "" || tok.ID == "" {
		t.Fatalf("空 token: %+v", tok)
	}

	dev, err := svc.EnrollDevice(ctx, tok.Token, "macbook")
	if err != nil {
		t.Fatalf("EnrollDevice: %v", err)
	}
	if dev.DeviceID == "" || dev.TunnelSecret == "" {
		t.Errorf("空字段: %+v", dev)
	}
}

func TestEnroll_TokenReplayRejected(t *testing.T) {
	ctx := context.Background()
	svc, _ := newSvc(t)

	tok, _ := svc.IssueToken(ctx, 0)
	if _, err := svc.EnrollDevice(ctx, tok.Token, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.EnrollDevice(ctx, tok.Token, "b"); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("二次使用应 ErrTokenInvalid, got %v", err)
	}
}

func TestEnroll_DeviceNameConflict(t *testing.T) {
	ctx := context.Background()
	svc, _ := newSvc(t)

	tok1, _ := svc.IssueToken(ctx, 0)
	tok2, _ := svc.IssueToken(ctx, 0)
	if _, err := svc.EnrollDevice(ctx, tok1.Token, "same"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.EnrollDevice(ctx, tok2.Token, "same"); !errors.Is(err, ErrDeviceExists) {
		t.Errorf("重名应 ErrDeviceExists, got %v", err)
	}
}

func TestEnroll_ExpiredToken(t *testing.T) {
	ctx := context.Background()
	svc, _ := newSvc(t)

	// 用注入时钟把"现在"拨到 token 过期后
	tok, _ := svc.IssueToken(ctx, time.Millisecond)
	svc.now = func() time.Time { return tok.ExpiresAt.Add(time.Hour) }

	if _, err := svc.EnrollDevice(ctx, tok.Token, "x"); !errors.Is(err, ErrTokenExpired) {
		t.Errorf("过期应 ErrTokenExpired, got %v", err)
	}
}

func TestEnroll_BadFormatRejected(t *testing.T) {
	ctx := context.Background()
	svc, _ := newSvc(t)
	if _, err := svc.EnrollDevice(ctx, "wrong-prefix", "n"); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("非法格式应 ErrTokenInvalid, got %v", err)
	}
}
