package server

import (
	"context"
	"errors"
	"sync"

	"github.com/zhiying8710/oh-my-lan/internal/store"
)

// ErrPortPoolExhausted 表示端口池已用尽。
var ErrPortPoolExhausted = errors.New("公网端口池已用尽")

// PortAllocator 从 [min, max] 范围分配未被服务占用的端口。
// 并发安全：所有 Allocate 调用串行，依赖 DB 的 UNIQUE 约束做最终兜底。
type PortAllocator struct {
	min, max int
	store    *store.Store
	mu       sync.Mutex
}

func NewPortAllocator(s *store.Store, min, max int) *PortAllocator {
	return &PortAllocator{min: min, max: max, store: s}
}

// Allocate 返回一个当前未被任何 service 占用的端口。
func (p *PortAllocator) Allocate(ctx context.Context) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	used, err := p.store.UsedPublicPorts(ctx)
	if err != nil {
		return 0, err
	}
	taken := make(map[int]struct{}, len(used))
	for _, port := range used {
		taken[port] = struct{}{}
	}
	for port := p.min; port <= p.max; port++ {
		if _, ok := taken[port]; !ok {
			return port, nil
		}
	}
	return 0, ErrPortPoolExhausted
}
