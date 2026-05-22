package logging

import (
	"sync"
	"time"
)

// LogEntry 是 RingBuffer 暴露给消费者的不可变快照。
// 字段命名与 slog 默认 attr 名一致，便于前端 / API 直接 stringify。
type LogEntry struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`   // INFO / WARN / ERROR / DEBUG
	Message string    `json:"message"`
	// Attrs 是 slog 的 key=val 扁平化字符串（"device=foo err=connection refused"）。
	// 不存 map 是因为 frontend 用纯字符串展示就够，省一层 marshal。
	Attrs string `json:"attrs,omitempty"`
}

// RingBuffer 是一个 thread-safe 环形日志缓冲。daemon/server 的 slog handler 把
// 每条 record 同步写一份到这里，admin API 反向拉取最近 N 条。
//
// 设计要点：
//   - 固定容量，环形覆盖。1000 条 ≈ 几百 KB，常驻内存可接受。
//   - 写入零分配（覆盖 slot），读取通过 Snapshot 一次性拷出，避免持锁期间被遍历。
//   - 与 slog 解耦：写入接口只接 LogEntry 三个字段，不依赖 slog 类型；
//     接 slog 的桥在 buffer_handler.go。
type RingBuffer struct {
	mu      sync.Mutex
	entries []LogEntry
	head    int  // 下一个写入位置
	full    bool // 已绕过一圈
	cap     int
}

func NewRingBuffer(cap int) *RingBuffer {
	if cap <= 0 {
		cap = 1000
	}
	return &RingBuffer{
		entries: make([]LogEntry, cap),
		cap:     cap,
	}
}

// Add 写一条 entry。覆盖最旧的一条。
func (b *RingBuffer) Add(e LogEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries[b.head] = e
	b.head = (b.head + 1) % b.cap
	if b.head == 0 {
		b.full = true
	}
}

// Snapshot 拷出最近 limit 条（按时间升序，老的在前）。limit ≤ 0 或大于实际容量时
// 返回全部。
func (b *RingBuffer) Snapshot(limit int) []LogEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := b.cap
	if !b.full {
		n = b.head
	}
	if limit <= 0 || limit > n {
		limit = n
	}
	out := make([]LogEntry, 0, limit)
	// 从最旧那条开始线性读 limit 条
	start := 0
	if b.full {
		start = b.head
	}
	// 偏移：要返回最后 limit 条，跳过 (n - limit) 条最老的
	skip := n - limit
	for i := skip; i < n; i++ {
		idx := (start + i) % b.cap
		out = append(out, b.entries[idx])
	}
	return out
}
