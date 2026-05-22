package logging

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// bufferHandler 是 slog.Handler 的实现，把每条 record 写进 RingBuffer。
// 通常与另一个真正写 stderr/file 的 handler 组合成 multiHandler 使用——单独用
// 不会有 stderr 输出。
//
// 设计要点：
//   - 仅做"读+往 buffer 写"，不直接落盘，因此可以稳定调（缓冲层不阻塞日志主路径）。
//   - Attrs 扁平化成 "k=v k=v" 字符串，避免把 map 暴露给上层 API；前端展示更省事。
type bufferHandler struct {
	level slog.Level
	buf   *RingBuffer
	// preformatted 是 With() 累积的 attrs，写出时拼到当条 record 的 attrs 前面
	preformatted string
}

// NewBufferHandler 构造写 ring buffer 的 slog.Handler。
func NewBufferHandler(buf *RingBuffer, level slog.Level) slog.Handler {
	return &bufferHandler{buf: buf, level: level}
}

func (h *bufferHandler) Enabled(_ context.Context, lvl slog.Level) bool {
	return lvl >= h.level
}

func (h *bufferHandler) Handle(_ context.Context, r slog.Record) error {
	var attrs strings.Builder
	if h.preformatted != "" {
		attrs.WriteString(h.preformatted)
	}
	r.Attrs(func(a slog.Attr) bool {
		if attrs.Len() > 0 {
			attrs.WriteByte(' ')
		}
		fmt.Fprintf(&attrs, "%s=%v", a.Key, a.Value.Any())
		return true
	})
	h.buf.Add(LogEntry{
		Time:    r.Time.UTC(),
		Level:   r.Level.String(),
		Message: r.Message,
		Attrs:   attrs.String(),
	})
	return nil
}

func (h *bufferHandler) WithAttrs(as []slog.Attr) slog.Handler {
	var sb strings.Builder
	sb.WriteString(h.preformatted)
	for i, a := range as {
		if sb.Len() > 0 || i > 0 {
			sb.WriteByte(' ')
		}
		fmt.Fprintf(&sb, "%s=%v", a.Key, a.Value.Any())
	}
	return &bufferHandler{level: h.level, buf: h.buf, preformatted: sb.String()}
}

func (h *bufferHandler) WithGroup(name string) slog.Handler {
	// 简化：把 group name 拼到 preformatted 前缀里。这不是严格的 slog group 语义——
	// 真正的 group 会让后续 With/Add 的 attrs 落到嵌套命名空间下。我们当前 oml server/daemon
	// 都不调 logger.WithGroup，所以这层错配不暴露；如果将来用了 group，再补真正实现。
	prefix := name + "."
	return &bufferHandler{level: h.level, buf: h.buf, preformatted: prefix + h.preformatted}
}

// multiHandler 把一条 record 复制给多个 handler。比 slog.NewMultiHandler 灵活，
// 因为每个 sub-handler 可以独立 Enabled 判断（同条 record 可能 INFO 进 stderr 但 WARN+ 进 buffer 之类）。
type multiHandler struct {
	hs []slog.Handler
}

// NewMultiHandler 把多个 slog.Handler 串联成单个，每条 record 复制到所有 enabled 的 handler。
func NewMultiHandler(handlers ...slog.Handler) slog.Handler {
	return &multiHandler{hs: handlers}
}

func (m *multiHandler) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range m.hs {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (m *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, h := range m.hs {
		if !h.Enabled(ctx, r.Level) {
			continue
		}
		if err := h.Handle(ctx, r.Clone()); err != nil {
			return err
		}
	}
	return nil
}

func (m *multiHandler) WithAttrs(as []slog.Attr) slog.Handler {
	out := make([]slog.Handler, len(m.hs))
	for i, h := range m.hs {
		out[i] = h.WithAttrs(as)
	}
	return &multiHandler{hs: out}
}

func (m *multiHandler) WithGroup(name string) slog.Handler {
	out := make([]slog.Handler, len(m.hs))
	for i, h := range m.hs {
		out[i] = h.WithGroup(name)
	}
	return &multiHandler{hs: out}
}
