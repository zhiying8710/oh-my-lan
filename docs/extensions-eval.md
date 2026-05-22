# 扩展评估：网络 / 流量统计 / 日志

> **2026-05 实施进度**：A1 + B1 + C1 部分实施。
> - **A1 修订为 A1'**：原计划读 chisel SessionStats 拿字节 → 实测 chisel v1.11.6 没暴露这层 hook（`share/tunnel/cnet.ConnCount` 只数连接、不数字节，server 也没有 OnSession 回调）。改成 **server-side TCP 探测器**：周期 dial 每个 enabled service 的 public_port，记 `last_probe_at` / `last_probe_ok`。UI 在 Forwards 表"链路"列展示。能回答的核心问题"forward 是否真通"达成；不能回答"传了多少字节"。
> - **B1 跳过**：与 A1 同源数据，chisel 不暴露 → 流量统计 schema 未建。等 chisel 接口扩展或迁 wireguard 再做。
> - **C1 完整实施**：`logging.RingBuffer` 1000 条环形 buffer + `/api/admin/logs` + 「服务端」tab 折叠区。
>
> 详细文件清单见本文档末尾"实施记录"。

## 触发场景

「服务端」tab 当前只有运行时 metrics + audit log。本评估考虑三个方向的扩展，分别给出现状、方案、成本、建议优先级。

每一项的目标读者是：日后想动手做的 你 自己。

---

## A. 网络（mesh 拓扑可视化 + 链路健康）

### 现状

- 服务端能知道**哪些设备在线**（last_seen_at + chisel session 活着）
- 能知道**哪个 forward 把谁映射到谁**（forwards 表）
- **不知道**：实际有没有流量、链路 RTT、丢包、bandwidth

### 用户实际需求

「设备 A 看到 B 在线，但 forward 实际不通」这个场景 — 现在排查只能 SSH 上设备看 daemon.stderr。如果服务端能展示拓扑 + 链路健康，排查不用上设备。

### 方案

**方案 A1：被动观察 (低成本)**
- chisel server 已有的 session info → 暴露 `bytes_sent / bytes_received` per device，可从 chisel `SessionStats()` API 抓取
- 每个 forward 上链路是否"近 1 分钟有字节" = 心跳判定
- UI 一个简单 ASCII / SVG 图：
  ```
  device-A ──[42 MB↑ / 18 KB↓]── server ──[18 KB↑ / 42 MB↓]── device-B (forward 8022→ssh)
  ```

**方案 A2：主动健康检查 (中成本)**
- daemon 每 30s 内部对所有 `Locals` 配置做一次 TCP connect probe（仅 connect 不发字节），上报到 server
- server 聚合：哪个 forward 最近 5 分钟内有过 connect 成功
- UI 在 forwards 表加一列「最近成功」/「连续失败次数」

**方案 A3：完整流量分析 (高成本)**
- 在 chisel client 这一层加 byte counter middleware，每条 connection 的 5-tuple + size 上报
- server 持久化，UI 出小时/天/周聚合
- 储存量：1 万条/天 × N 设备 = MB 级，SQLite 能扛

### 成本

| 方案 | dev 工时 | 维护负担 | 新依赖 |
|---|---|---|---|
| A1 被动观察 | 1 天 | 低 | 无 |
| A2 主动探测 | 2-3 天 | 中（daemon 心跳路径多一条） | 无 |
| A3 完整流量 | 1 周 | 高（DB schema + retention） | 无 |

### 建议

**做 A1，跳过 A2/A3。**

A1 的"实际有没有流量"信号已经能解掉 80% 的排查场景。A2 在 chisel 已经做半连接保活的前提下重复性高。A3 是分析需求，对单人 mesh 而言数据量小到看 audit log + ss/netstat 就够了，不值得维护 DB schema。

---

## B. 流量统计

### 现状

无流量计量。

### 用户实际需求

- 想知道：mesh 上是不是有人在大量传文件 (rsync via forward)
- 想知道：VPS 的带宽 quota 是不是被耗光（公有云 VPS 通常 1-5TB / 月）

### 方案

**方案 B1：从 chisel 拿 byte counter (低成本)**
- 与 A1 是同一份数据，分两种 UI 展示：拓扑视角（per-link）vs 时间序列（per-month）
- SQLite 加 `daily_traffic` 表：date + device_id + bytes_in + bytes_out
- 每天 00:00 rollover 一次（reaper goroutine）

**方案 B2：每 forward 单独计费 (中成本)**
- B1 之上加 forward_id 维度
- UI: 「Forward」tab 加 "本月流量" 列

### 成本

| 方案 | dev 工时 | 维护负担 |
|---|---|---|
| B1 per-device 日统计 | 2 天 | 低 |
| B2 per-forward 流量 | 3 天 | 中 |

### 建议

**做 B1 跟 A1 一起做。** B2 留作 future——单人 mesh 通常不需要这么细。

VPS 带宽监控真正可靠的方式是装 vnstat 在 server 上，跟应用解耦。oh-my-lan UI 不必复刻 vnstat。

---

## C. 日志

### 现状

- server: 结构化 slog，写 stderr，由 systemd 管 (`journalctl -u oml-server`)
- daemon: 类似，写 `<data_dir>/daemon.stderr` 或 launchd/systemd StandardError
- audit: SQLite 表（200 条 ring buffer），UI 已展示

### 用户实际需求

- 排查时不想 ssh 进 VPS `journalctl` —— 在 Web UI 看 server 日志
- daemon 日志只在设备本机，远程看不到

### 方案

**方案 C1：server 日志暴露到 admin API (中成本)**
- server 内部 logger 多一个 sink：写到一个 ring buffer（1000 条）
- `GET /api/admin/logs` 返回最近 N 条（按 level filter）
- UI 「服务端」tab 加一个折叠区「最近日志」，旁边 audit log

**方案 C2：daemon 日志回传 (中-高成本)**
- daemon 在 reload bootstrap 时附带"最近 N 条 log"上传
- server 持久化到 `daemon_logs` 表
- 数据量、隐私 (本机 hostname / 文件路径)、需要权衡

**方案 C3：什么都不动**
- server: 一直可以 `journalctl -u oml-server -n 100`
- daemon: 本机 daemon.stderr 已经够
- 如果未来需要远程汇集，上 Loki / Vector 之类专业方案

### 成本

| 方案 | dev 工时 | 维护负担 |
|---|---|---|
| C1 server logs in UI | 1-2 天 | 低 |
| C2 daemon logs 回传 | 1 周 | 高 |
| C3 不做 | 0 | 0 |

### 建议

**做 C1，跳过 C2，C3 是兜底**。

C1 的 UX 收益明显——以后排查 chisel 连接问题不用切 SSH。C2 是远程登录设备的反模式，且涉及隐私（设备本地 path 可能包含个人信息），对单人 mesh 价值低。

---

## 综合优先级建议

1. **A1 + B1（合并一次实现）**：chisel SessionStats → server SQL → UI 拓扑 + 日流量
2. **C1**：server 日志环形 buffer + admin endpoint + UI 区
3. （半年后再看）A2 主动探测

不建议做：A3 完整流量、B2 per-forward、C2 daemon 回传。

---

## 落地路径草图

如果将来做 A1+B1+C1，按下面的 milestone 拆：

- **M-X.1**：chisel session stats hook + SQLite 表 `device_traffic_daily(date, device_id, in, out)`
- **M-X.2**：admin endpoint `GET /api/admin/topology`（设备 + 流量 + last_seen）
- **M-X.3**：UI 服务端 tab 加 ASCII / SVG 拓扑图（vanilla canvas，零依赖）
- **M-X.4**：server logger 二号 sink + ring buffer
- **M-X.5**：admin endpoint `GET /api/admin/logs?level=&limit=`
- **M-X.6**：UI 服务端 tab 加日志折叠区，复用现有 `<details>` 模式
- **M-X.7**：daily reaper 翻日 → 同步触发 metrics rollover

每个 milestone 半天到一天的工作量。

---

## 实施记录（2026-05）

### A1' 链路健康探测
- `internal/store/migrations/0007_link_health.sql`：services 表加 `last_probe_at` + `last_probe_ok` 两列
- `internal/store/service.go`：`RecordServiceProbe()` + `ListServicesJoined()` 字段扩展
- `internal/server/healthprober.go`：45s 周期 goroutine，并发 dial 所有 enabled service 的 127.0.0.1:public_port
- `internal/proto/dto.go`：`AdminServiceDTO` 加 `LastProbeAt` + `LastProbeOK`
- 前端 `forwards` tab 第 5 列「链路」徽章；UDP 服务标"不探测"

### C1 服务端日志
- `internal/logging/buffer.go`：thread-safe RingBuffer + LogEntry
- `internal/logging/buffer_handler.go`：bufferHandler (slog.Handler 实现) + multiHandler 把 stderr + buffer 同时作为 sink
- `internal/logging/slog.go`：`NewWithBuffer()` API
- `cmd/omlserver/main.go`：runServer 构造 logger 时传 RingBuffer
- `internal/server/handler_logs.go`：`GET /api/admin/logs?limit=` 返回 snapshot
- 前端「服务端」tab 「服务端日志」折叠区 + 刷新按钮 + level 染色

### B1 未实施 — 跳过原因
chisel 不暴露 per-session 字节统计；强行实现需 fork chisel 或在 ssh 层插 io 包装，
工作量 > 现实价值。文档保留方案设计供将来 chisel 升级时回填。
