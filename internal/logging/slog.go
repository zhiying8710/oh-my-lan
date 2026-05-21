// Package logging 提供基于标准库 log/slog 的轻量封装。
// 该包是叶子包，不依赖项目内任何其它包，便于在任何层使用。
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// Options 描述 logger 构建参数。
type Options struct {
	Level  string // debug / info / warn / error；空或未知值回落到 info
	Format string // text / json；其它值回落到 text
}

// New 构建写到 stderr 的 *slog.Logger。
func New(opts Options) *slog.Logger {
	return NewTo(os.Stderr, opts)
}

// NewTo 构建写到指定 Writer 的 *slog.Logger。daemon 模式下若 stderr 不可用
// （例如 Windows GUI 父进程 spawn 的 console-detached 子进程），可以把它指向一个
// `os.OpenFile(...)` 拿到的 file handle，让运行时日志可调试。
func NewTo(w io.Writer, opts Options) *slog.Logger {
	handlerOpts := &slog.HandlerOptions{Level: parseLevel(opts.Level)}

	var handler slog.Handler
	switch strings.ToLower(opts.Format) {
	case "json":
		handler = slog.NewJSONHandler(w, handlerOpts)
	default:
		handler = slog.NewTextHandler(w, handlerOpts)
	}
	return slog.New(handler)
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
