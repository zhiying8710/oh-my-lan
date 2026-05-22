package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestNormalizeLocalAddr 覆盖纯端口、:port、host:port、IPv6、负面用例。
// 拆包前在 server/helpers_test.go，随 NormalizeLocalAddr 迁出后同步迁过来。
func TestNormalizeLocalAddr(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"22", "127.0.0.1:22", false},
		{"8080", "127.0.0.1:8080", false},
		{"65535", "127.0.0.1:65535", false},
		{":22", "127.0.0.1:22", false},
		{":8080", "127.0.0.1:8080", false},
		{"127.0.0.1:22", "127.0.0.1:22", false},
		{"192.168.1.10:8096", "192.168.1.10:8096", false},
		{"localhost:80", "localhost:80", false},
		{"my-nas.local:445", "my-nas.local:445", false},
		{"[::1]:22", "[::1]:22", false},
		{"[fe80::1]:80", "[fe80::1]:80", false},
		{"  22  ", "127.0.0.1:22", false},
		{"", "", true},
		{"   ", "", true},
		{"0", "", true},
		{"65536", "", true},
		{"-1", "", true},
		{":abc", "", true},
		{"host:abc", "", true},
		{"127.0.0.1", "", true},
		{"hostname", "", true},
	}
	for _, c := range cases {
		got, err := NormalizeLocalAddr(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("NormalizeLocalAddr(%q) err=%v wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if !c.wantErr && got != c.want {
			t.Errorf("NormalizeLocalAddr(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestWithCORS_PreflightShortCircuit: OPTIONS 不应进 next handler，直接 204。
func TestWithCORS_PreflightShortCircuit(t *testing.T) {
	nextHit := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { nextHit = true })

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/api/admin/info", nil)
	WithCORS(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("OPTIONS 应 204, got %d", rr.Code)
	}
	if nextHit {
		t.Errorf("OPTIONS 不应到 next handler")
	}
	if h := rr.Header().Get("Access-Control-Allow-Origin"); h != "*" {
		t.Errorf("Allow-Origin: %q", h)
	}
}

// TestWithCORS_PassthroughAddsHeaders：非 OPTIONS 请求应继续到 next 且仍带 CORS 头。
func TestWithCORS_PassthroughAddsHeaders(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hi"))
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/info", nil)
	WithCORS(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK || rr.Body.String() != "hi" {
		t.Errorf("next 没跑：code=%d body=%q", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Errorf("缺少 Allow-Methods 头")
	}
}
