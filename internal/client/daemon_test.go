package client

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zhiying8710/oh-my-lan/internal/tunnel"
)

func TestHashSpecs_StableAndDifferentOnChange(t *testing.T) {
	rA := []tunnel.Remote{
		{PublicPort: 40001, LocalAddr: "127.0.0.1:22", Protocol: "tcp"},
		{PublicPort: 40002, LocalAddr: "127.0.0.1:80", Protocol: "tcp"},
	}
	rB := []tunnel.Remote{
		{PublicPort: 40002, LocalAddr: "127.0.0.1:80", Protocol: "tcp"},
		{PublicPort: 40001, LocalAddr: "127.0.0.1:22", Protocol: "tcp"},
	}
	if hashSpecs(rA, nil) != hashSpecs(rB, nil) {
		t.Error("remote 顺序不应影响 hash")
	}

	lA := []tunnel.Local{
		{LocalPort: 8022, RemotePublicPort: 40001, Protocol: "tcp"},
		{LocalPort: 8080, RemotePublicPort: 40002, Protocol: "tcp"},
	}
	lB := []tunnel.Local{lA[1], lA[0]}
	if hashSpecs(rA, lA) != hashSpecs(rA, lB) {
		t.Error("local 顺序不应影响 hash")
	}

	// 新增一个 local 应改 hash
	if hashSpecs(rA, nil) == hashSpecs(rA, lA) {
		t.Error("加入 locals 应改 hash")
	}

	// remote 变化应改 hash
	rC := append([]tunnel.Remote(nil), rA...)
	rC[0].PublicPort = 40099
	if hashSpecs(rA, lA) == hashSpecs(rC, lA) {
		t.Error("remote 变化应改 hash")
	}
}

// 验证 daemon 在 bootstrap 返回 401 时立刻退出，不进入无限重试。
func TestDaemon_ExitsOnUnauthorized(t *testing.T) {
	// 自起一个永远返回 401 的 mock server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"revoked"}`))
	}))
	defer ts.Close()

	state := &State{
		ServerURL:    ts.URL,
		DeviceID:     "fake",
		DeviceName:   "fake",
		TunnelSecret: "fake",
		ChiselAddr:   "127.0.0.1:1",
	}
	d := NewDaemon(state, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := d.Run(ctx)
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("Run() 应返回 ErrUnauthorized, got %v", err)
	}
	if ctx.Err() != nil {
		// 如果是 ctx timeout 触发的退出，说明 daemon 没有自杀
		t.Errorf("daemon 没有及时退出，被 ctx timeout 兜底；这意味着 401 没短路")
	}
}

func TestNormalizeChiselURL(t *testing.T) {
	cases := map[string]string{
		"vps:8443":               "http://vps:8443",
		"http://vps:8443":        "http://vps:8443",
		"https://vps:8443":       "https://vps:8443",
		"127.0.0.1:18443":        "http://127.0.0.1:18443",
	}
	for in, want := range cases {
		if got := normalizeChiselURL(in); got != want {
			t.Errorf("normalize(%q)=%q want %q", in, got, want)
		}
	}
}
