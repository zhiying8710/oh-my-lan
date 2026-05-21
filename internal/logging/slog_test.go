package logging

import (
	"log/slog"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"":        slog.LevelInfo,
		"info":    slog.LevelInfo,
		"INFO":    slog.LevelInfo,
		"debug":   slog.LevelDebug,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"weird":   slog.LevelInfo,
	}
	for in, want := range cases {
		if got := parseLevel(in); got != want {
			t.Errorf("parseLevel(%q)=%v want %v", in, got, want)
		}
	}
}

func TestNew_DoesNotPanic(t *testing.T) {
	for _, opts := range []Options{
		{Level: "info", Format: "text"},
		{Level: "debug", Format: "json"},
		{Level: "", Format: ""},
		{Level: "garbage", Format: "garbage"},
	} {
		logger := New(opts)
		if logger == nil {
			t.Fatalf("New(%+v) 返回 nil", opts)
		}
	}
}
