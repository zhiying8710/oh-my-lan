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
	return slog.New(makeHandler(w, opts))
}

// NewWithBuffer 构建一个同时写 Writer + ring buffer 的 logger。
// 调用方拿到 RingBuffer 后可暴露给 admin /api/admin/logs，让 Web UI 实时拉最近日志。
//
// 用法 (omlserver)：
//
//	buf := logging.NewRingBuffer(1000)
//	logger := logging.NewWithBuffer(os.Stderr, logging.Options{...}, buf)
//	// ... 把 buf 暴露给 server，让 admin handler 调 buf.Snapshot(N)
func NewWithBuffer(w io.Writer, opts Options, buf *RingBuffer) *slog.Logger {
	level := parseLevel(opts.Level)
	return slog.New(NewMultiHandler(
		makeHandler(w, opts),
		NewBufferHandler(buf, level),
	))
}

func makeHandler(w io.Writer, opts Options) slog.Handler {
	handlerOpts := &slog.HandlerOptions{Level: parseLevel(opts.Level)}
	switch strings.ToLower(opts.Format) {
	case "json":
		return slog.NewJSONHandler(w, handlerOpts)
	default:
		return slog.NewTextHandler(w, handlerOpts)
	}
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
