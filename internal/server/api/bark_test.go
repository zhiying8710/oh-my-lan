package api

import (
	"testing"
	"time"
)

// TestFormatRelativeTime 覆盖 5 个时间范围。
// 历史：拆包时 server/bark_test.go 被删（FormatRelativeTime 从 server pkg 迁到 api pkg），
// 用例没跟过来，5 个 case 的覆盖丢失。此处补回。
func TestFormatRelativeTime(t *testing.T) {
	cases := []struct {
		name string
		t    time.Time
		want string
	}{
		{"zero → never", time.Time{}, "从未上线"},
		{"45s → 45 秒前", time.Now().Add(-45 * time.Second), "45 秒前"},
		{"3m → 3 分钟前", time.Now().Add(-3 * time.Minute), "3 分钟前"},
		{"2h → 2 小时前", time.Now().Add(-2 * time.Hour), "2 小时前"},
		{"3d → 3 天前", time.Now().Add(-72 * time.Hour), "3 天前"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := FormatRelativeTime(c.t)
			if got != c.want {
				t.Fatalf("want %q, got %q", c.want, got)
			}
		})
	}
}

// TestSendBarkPush_RejectsEmptyBase 防御：空 base URL 应早 fail 而非把 panic 传给 caller。
func TestSendBarkPush_RejectsEmptyBase(t *testing.T) {
	err := SendBarkPush(t.Context(), "", "title", "body")
	if err == nil {
		t.Fatal("空 base URL 应返回 err")
	}
}
