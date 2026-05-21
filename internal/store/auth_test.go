package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAdminUserCRUD(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := time.Now()

	u := AdminUser{
		ID: "u1", Username: "alice", PasswordHash: "$argon2id$v=19$m=1$x$y",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.CreateAdminUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAdminUser(ctx, AdminUser{ID: "u2", Username: "alice", PasswordHash: "x", CreatedAt: now, UpdatedAt: now}); err == nil {
		t.Error("username UNIQUE 应触发")
	}

	got, err := s.GetAdminUserByUsername(ctx, "alice")
	if err != nil || got.ID != "u1" {
		t.Errorf("get by username: %v %+v", err, got)
	}
	got, err = s.GetAdminUserByID(ctx, "u1")
	if err != nil || got.Username != "alice" {
		t.Errorf("get by id: %v %+v", err, got)
	}

	if err := s.UpdateAdminUserPassword(ctx, "alice", "new-hash"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetAdminUserByUsername(ctx, "alice")
	if got.PasswordHash != "new-hash" {
		t.Errorf("update password: got %s", got.PasswordHash)
	}

	if err := s.UpdateAdminUserPassword(ctx, "no-such", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的 user 应 ErrNotFound, got %v", err)
	}

	n, _ := s.CountAdminUsers(ctx)
	if n != 1 {
		t.Errorf("count: %d", n)
	}

	if err := s.DeleteAdminUser(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAdminUserByUsername(ctx, "alice"); !errors.Is(err, ErrNotFound) {
		t.Errorf("删除后应 ErrNotFound, got %v", err)
	}
}

func TestSessionsCRUDAndExpiry(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := time.Now()

	if err := s.CreateAdminUser(ctx, AdminUser{
		ID: "u", Username: "u", PasswordHash: "x", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	live := Session{
		ID: "s1", UserID: "u", TokenHash: "h1",
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	expired := Session{
		ID: "s2", UserID: "u", TokenHash: "h2",
		CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour),
	}
	if err := s.CreateSession(ctx, live); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(ctx, expired); err != nil {
		t.Fatal(err)
	}

	// 活跃 session 能拿到
	got, err := s.GetActiveSessionByHash(ctx, "h1", now)
	if err != nil || got.ID != "s1" {
		t.Errorf("get active live: %v %+v", err, got)
	}
	// 过期 session 拿不到（用 GetActiveSessionByHash 过滤）
	if _, err := s.GetActiveSessionByHash(ctx, "h2", now); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired 应 ErrNotFound, got %v", err)
	}

	// Touch 更新 last_used_at
	if err := s.TouchSession(ctx, "s1", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	// reaper：清掉 1 条过期
	n, err := s.DeleteExpiredSessions(ctx, now)
	if err != nil || n != 1 {
		t.Errorf("DeleteExpired: n=%d err=%v", n, err)
	}

	// 显式 delete by hash
	if err := s.DeleteSessionByHash(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetActiveSessionByHash(ctx, "h1", now); !errors.Is(err, ErrNotFound) {
		t.Errorf("删除后应 ErrNotFound, got %v", err)
	}
}

func TestSessionCascadeOnUserDelete(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := time.Now()
	_ = s.CreateAdminUser(ctx, AdminUser{ID: "u", Username: "u", PasswordHash: "x", CreatedAt: now, UpdatedAt: now})
	_ = s.CreateSession(ctx, Session{ID: "s", UserID: "u", TokenHash: "h", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})

	_ = s.DeleteAdminUser(ctx, "u")
	if _, err := s.GetActiveSessionByHash(ctx, "h", now); !errors.Is(err, ErrNotFound) {
		t.Errorf("cascade 应删 session, got %v", err)
	}
}
