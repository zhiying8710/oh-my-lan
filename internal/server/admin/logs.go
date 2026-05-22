// admin/logs.go: GET /api/admin/logs?limit=N — 暴露 server 进程的最近日志。
// 没接 LogBuffer 时返回空数组（200 OK，不算错）。

package admin

import (
	"net/http"
	"strconv"

	"github.com/zhiying8710/oh-my-lan/internal/logging"
	"github.com/zhiying8710/oh-my-lan/internal/proto"
	"github.com/zhiying8710/oh-my-lan/internal/server/api"
)

// logBuf 经 LogBufFn 解引用拿到当前 ring buffer；nil-safe（未注入时 / fn 自己返回 nil）。
func (h *Handler) logBuf() *logging.RingBuffer {
	if h.LogBufFn == nil {
		return nil
	}
	return h.LogBufFn()
}

// GET /api/admin/logs?limit=N
//
// 返回 server 进程的最近 N 条日志（来自 logging.RingBuffer）。
// 默认 200，最多 1000（与 buffer 容量一致；再大无意义）。
//
// TODO: 当前只支持 limit。原 extensions-eval 提到的 ?level= 和 ?since= 留待真实需求出现再加——
// 前端目前只有"展开折叠区一次性拉 200 条"这一种用法，过滤可在 client-side 做。
func (h *Handler) HandleAdminLogs(w http.ResponseWriter, r *http.Request) {
	buf := h.logBuf()
	if buf == nil {
		api.WriteJSON(w, http.StatusOK, proto.LogsResponse{Entries: []proto.LogEntryDTO{}})
		return
	}
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > 1000 {
				n = 1000
			}
			limit = n
		}
	}
	snap := buf.Snapshot(limit)
	out := proto.LogsResponse{Entries: make([]proto.LogEntryDTO, 0, len(snap))}
	for _, e := range snap {
		out.Entries = append(out.Entries, proto.LogEntryDTO{
			Time:    e.Time,
			Level:   e.Level,
			Message: e.Message,
			Attrs:   e.Attrs,
		})
	}
	api.WriteJSON(w, http.StatusOK, out)
}
