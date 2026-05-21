# oh-my-lan 架构设计

> 本文档归档于 M0 阶段，记录项目的核心需求、关键决策与里程碑。后续阶段如有方案变更，应同步更新本文。

## 1. 目标与范围

在个人公网 VPS 上搭建一套以 chisel 为底层传输的隧道控制系统，满足：

- 仅服务于本人，规模 < 10 台设备、< 100 个服务
- 支持任意 TCP/UDP 服务
- 两类访问方向：
  - **reverse 发布**：客户端本地服务 → 公网端口
  - **mesh 互联**：客户端 A → 客户端 B 的本地服务（流量经 VPS 中转）
- 客户端覆盖 macOS / Linux / Windows，Linux 需提供 headless CLI 模式
- 新设备一行命令接入；服务发布、规则变更热生效

## 2. 关键决策

| 议题 | 决策 | 理由 |
| --- | --- | --- |
| 路由方式 | 每服务一公网端口，端口池 `40000-49999` | 纯 L4 不可能按名字路由；与"任意 TCP/UDP"一致 |
| 控制平面 | 纯 HTTP（不上域名/TLS） | 用户决定，承担 enrollment 链路明文风险 |
| 接入认证 | 每设备一次性 enrollment token + ed25519 设备密钥 | 一次性 token 即使被截也只能用一次 |
| 后端实现 | 单 Go 二进制，chisel 作为库嵌入 | 控制平面、状态机、路由表完全自有 |
| 客户端实现 | Go daemon + Tauri 桌面壳 + CLI | 桌面壳轻量；Linux headless 直接禁用壳 |
| daemon ↔ UI/CLI IPC | Unix socket / Windows Named Pipe | 比 localhost HTTP 更安全 |
| 数据存储 | SQLite + 内存缓存 | 个人规模够用 |

## 3. 总体架构

```
┌─────────────────── VPS（公网） ───────────────────┐
│  omlserver (Go single binary)                    │
│  ├─ Control Plane（HTTP API）                     │
│  ├─ Chisel Session Manager（lib 模式）            │
│  ├─ Port Allocator（40000-49999）                 │
│  ├─ Router (TCP/UDP listener × N)                │
│  ├─ Mesh Relay（A↔Server↔B）                      │
│  ├─ State Store (SQLite)                         │
│  └─ Embedded Web Admin UI                        │
└──────────────────────────────────────────────────┘
        ▲ WSS（chisel session + 控制 RPC 复用）
        │
   ┌────┴────┐ ┌────────┐ ┌────────┐
   │ MacBook │ │  NAS   │ │ Win PC │
   │ daemon  │ │ daemon │ │ daemon │
   │ +Tauri  │ │ +CLI   │ │ +Tauri │
   └─────────┘ └────────┘ └────────┘
```

## 4. 模块设计

### 4.1 控制 / 数据通道复用

- chisel session 提供加密数据通道（SSH over WebSocket）
- 同一条连接上跑轻量 JSON-RPC 承载心跳、规则下发、状态上报
- 不开第二条 TCP 连接，避免 NAT 后两条连接生命周期不一致

### 4.2 服务模型

```
device          : id, name, fingerprint, status, last_seen, labels
service         : id, owner_device_id, name, protocol, local_addr,
                  expose ∈ {public(port), mesh(allow_devices)}, enabled
enrollment_token: id, token_hash, expires_at, used_at, used_by_device_id
audit_log       : device_id, action, payload, ts
```

服务可同时具备 `public` 与 `mesh` 两种 expose。

### 4.3 端口分配

- 配置 `port_pool: 40000-49999`
- 启动时校验 VPS 上是否被系统其它进程占用
- 删除服务后端口立即回收

### 4.4 断线自愈

- 客户端：指数退避重连（1s → 60s 上限），30s 心跳
- 服务端：session drop 后保留路由规则 5 分钟 grace；同 fingerprint 重连直接绑回原规则，TCP listener 不重启
- 超 grace 标 offline，内存缓存清，DB 规则保留

### 4.5 mesh 路由

```
[Client A]                  [Server]                 [Client B]
本地 :8022 listener  ─►  接收, 查表找 B, 在 B 的 session
                          上开 stream → io.Copy   ─►  本地 dial 127.0.0.1:22
```

### 4.6 UDP 风险

chisel 的 UDP 经 UDP-over-TCP 隧道，对实时音视频/游戏延迟敏感场景体验差，对 DNS、syslog、WireGuard handshake 等可接受。M4 单独里程碑验证。

### 4.7 桌面 UI

- Tauri 壳调用本机 daemon（Unix socket / Named Pipe）
- daemon 完全独立二进制，Linux headless 模式不打包 Tauri 壳
- 同一套 control plane API 服务 Web Admin / Tauri UI / CLI，禁止 CLI 绕过 API 直写 DB

## 5. 仓库结构

```
oh-my-lan/
├── cmd/
│   ├── omlserver/    # 服务端二进制
│   └── omlctl/       # 客户端 daemon + CLI（同一二进制，多子命令）
├── internal/
│   ├── config/       # 配置加载
│   ├── logging/      # slog 封装
│   ├── version/      # ldflags 注入
│   ├── proto/        # (M1+) 控制面 RPC 协议
│   ├── tunnel/       # (M1+) chisel 封装
│   ├── router/       # (M1+) 端口分配、TCP/UDP listener、mesh relay
│   ├── store/        # (M1+) SQLite 数据访问
│   ├── enroll/       # (M1+) token 与设备注册
│   └── auth/         # (M1+) 设备密钥
├── ui/
│   ├── admin/        # (M5) Web Admin UI
│   └── desktop/      # (M5) Tauri 客户端
├── configs/          # 示例配置
└── docs/             # 文档
```

## 6. 里程碑

| 里程碑 | 内容 | 验收 |
| --- | --- | --- |
| M0 ✅ | 仓库骨架、CI、配置/日志/版本基础包 | `make build` 出两个二进制；CI 三平台绿 |
| M1 ✅ | 控制平面 + enrollment + 单服务 TCP 发布 | 端到端 echo 转发通；服务端重启后 fingerprint 持久；daemon 自动重连 |
| M2 ✅ | 多服务、热 reload、device 状态流转、CLI 全量 | daemon 运行中加/删/停服务自动 reload；device list 显示 online；enable/disable 通过 API |
| M3 ✅ | mesh 客户端互访（TCP） | A 的本机端口经 chisel L: 转发到 server，再经 R: 到 B 本地；forward CRUD 热生效 |
| M4 ✅ | UDP 支持验证 | public 路径 + mesh 路径 + 真实 DNS 查询全部通过 |
| M5.1 ✅ | Web Admin UI（只读视图） | embed.FS 嵌入静态资源；admin token 认证；浏览器查看 devices/services/forwards/info |
| M5.2.X ✅ | Web Admin 写视图 | admin 代发服务/forward、enable/disable、删除、revoke device 即时生效、远程签发 enrollment token；同时清掉两条历史限制 |
| M5.2.Y ✅ | Tauri 桌面壳 | webview 加载 Web Admin（共享同一份 web 资源）；额外暴露 daemon_start/stop/status IPC；自适应「本机」tab；CORS 放开支持跨源 fetch |
| P1+P2+P3 ✅ | 健康加固 | server 包分层（10 文件单一职责）、N+1 → JOIN、`IsUniqueViolation` 类型安全、Tauri Rust 单测、metrics 端点、audit_log 表 + UI 审计 tab |
| P+ ✅ | 账号密码登录 + session | argon2id 哈希、`/api/auth/{login,logout,me}`、middleware 双轨（session 优先 + admin token 兼容）、过期 reaper、UI 改为用户名密码、CLI `admin user set/list/delete` |
| M5 | Web Admin UI + Tauri 桌面 UI | UI 上能完成 enroll/publish/revoke |
| M6 | 观测性、审计日志、自动备份 | 故障可定位，配置可恢复 |

## 7. 已知风险

1. **VPS 端口段开放**：依赖云厂商允许批量开放 `40000-49999`。已确认可用。
2. **控制面明文**：纯 HTTP 决策的明确取舍，后续若需要可升级为自签 TLS + 指纹固定。
3. **mesh 中转带宽**：A→B 流量全经 VPS，吃出口带宽。个人规模可接受。
4. **UDP-over-TCP 性能**：见 4.6。
5. **Windows 服务化**：daemon 需以 Windows Service 形式自启，CLI 需要 `service install/uninstall`。
6. **DB 迁移**：从 M1 起即引入 migration 工具，禁止手动 `ALTER`。

## 8. 已知限制

- **运行中新增/删除服务/forward 的生效延迟**：M2/M3 通过"daemon 轮询 bootstrap 后重启 chisel client"实现热 reload，默认 30 秒一次（`reload_interval_seconds` 可配）。变更生效时业务连接短暂中断 < 1s。这是 chisel client 设计上 specs 静态的代价；后续若自实现路由层可消除中断。
- **mesh 流量必须 server + B 双在线**：A forward B 的服务时，A 的 chisel L: → server → server R: listener → B 的 chisel client，链路上 server 和 B 任一离线则 8022 拨号失败。这是 mesh 的天然约束。
- ~~**forward 没有 enable/disable**~~：M5.2.X 已补齐 `omlctl forward enable/disable` 和对应 API。
- ~~**handleListForwards / handleListAllServices / Admin 系列存在 N+1 query**~~：已由 P1.B 通过 `ListServicesJoined` / `ListForwardsJoined` / `ListDevicesWithCounts` 三个 JOIN 查询消除。
- ~~**Web Admin UI 只读**~~：M5.2.X 已补齐写视图。
- **Web Admin UI 走纯 HTTP**：与控制平面相同，admin token 明文传输。这是 M0 决策"控制面纯 HTTP"的延续。可后续升级为自签 TLS。
- **Tauri spawned daemon orphan 风险**：如果 Tauri 进程崩溃或被 `kill -9`，由它 spawn 的 omlctl daemon 子进程会变成孤儿，继续在后台跑。`daemon_stop` 是显式调用才会停。要彻底防止 orphan，用户应通过 systemd/launchd 管 daemon，不通过 Tauri。
- **Tauri 不签名**：macOS 首次启动需用户右键打开放行；这是用户的取舍。`xattr -dr com.apple.quarantine *.app` 可一次性消除警告。
- **Tauri 端 daemon 日志不在 UI 显示**：spawn 时 stdout/stderr 走 `Stdio::null`。要看日志请用 CLI 直接跑或交给 systemd/launchd。
- ~~**device revoke 不立刻撤销 chisel session**~~：M5.2.X 通过 `POST /api/admin/devices/{id}/revoke` 实现：DB 删 + `tunnel.RemoveDevice` 同步执行 + daemon 检测 401 后主动退出 → 隧道在一个 reload 周期内彻底失活。CLI 的 `omlserver device revoke` 仍是只删 DB 的本地命令，**面向运行中 server 的撤销请用 admin API**。
- **daemon 收到 401 即自杀**：admin revoke 后受影响 daemon 立刻退出。这是预期；若 daemon 由 systemd 等管理，会被自动重启；重启后凭证已失效，仍 401，仍退出——形成 "撤销 → 自动停" 的循环（不会消耗 CPU，因为退出是真退出，systemd 默认 RestartSec=100ms 也会有较长 backoff）。
- **enrollment token 字符串匹配 SQLite UNIQUE 错误**：`internal/server/handlers.go` 用 `strings.Contains` 判 SQLite 唯一约束错误；后续可引入 modernc.org/sqlite 错误码做精确判断。
- **tunnel_secret 明文存 SQLite**：按 SSH 私钥级别保护，依赖 0o600 文件权限；chisel 内置认证要求明文，无 hash 存储路径。
- **requireLocal 用 RemoteAddr 字符串解析**：对反向代理后场景不准；当前 token 签发要求 server 本机操作，无反代场景。
- **chisel client / server 用日志库 cio.Logger**：与项目 slog 日志风格不一致，但 cio 是 chisel 库内部 API，替换成本高，保持现状。

## 9. 端到端验证记录

### M1 链路

1. omlserver 启动监听控制 API + chisel 入口
2. `omlserver token create` 本地生成一次性 token
3. `omlctl enroll --server --token --name` 注册并落盘 state.json
4. `omlctl service add --name --proto tcp --local 127.0.0.1:N` 发布服务
5. `omlctl daemon start` 起隧道
6. 公网端口 → 隧道 → 本地服务 → 回环 echo 验证完整
7. kill server 后重启，daemon 自动重连，转发恢复

### M2 增量

8. daemon 运行中调用 `service add` 新增第二个服务，等待 reload 周期后第二个公网端口可达
9. `omlserver device list` 显示设备 status=online + last_seen 实时更新
10. `omlctl service disable <id>` 停用后，reload 周期内对应公网端口转发被切断

### M3 增量（mesh）

11. 设备 B 发布 echo 服务（B 19999 → public 41000）+ daemon 在线
12. 设备 A 通过 `service list --all` 看到 B 的服务，取得 service id
13. A `forward add --service <id> --local 8022` 创建 forward，A daemon reload 后 127.0.0.1:8022 监听
14. `nc 127.0.0.1 8022` ← echo 流量经 A 的 chisel session → server → B 的 chisel session → B 本地 echo
15. A `forward rm <id>` 删除 forward，reload 周期内 8022 不再可用，但 B 的 41000 public 仍可达

### M4 增量（UDP）

16. B 发布 UDP echo（19998/udp → public 41000/udp），Python UDP client 经 public 端口能收到 `UECHO:...` 回包
17. A 通过 forward 把 B 的 UDP echo 拉到本机 18888/udp，mesh 链路 UDP 包正确往返
18. B 发布 mini DNS responder（15353/udp），A forward 到 25353/udp，`dig +short @127.0.0.1 -p 25353 example.com` 返回 1.2.3.4

> chisel 的 UDP 是基于 UDP-over-TCP 隧道。对小包请求-响应（DNS、syslog、WireGuard handshake）体验良好；对实时音视频/游戏类延迟敏感场景不推荐。

### M5.1 增量（Web Admin UI）

19. `omlserver admin token create` 生成 `oat_xxx` 长期凭证（token_hash 落 DB，明文只回显一次）
20. 浏览器 `/admin/` 进入登录页 → 输入 token → fetch `/api/admin/info` 探针成功 → 进主视图
21. 切换 devices/services/forwards/info tab，分别看到 list 与 brief；`hover` 行高亮
22. `omlserver admin token revoke <id>` 后，已登录浏览器下次 fetch 直接 401，前端自动回登录页
23. 所有 admin 端点既支持 `Authorization: Bearer` 也支持 `Cookie: oml_admin=`

### M5.2.X 增量（Web Admin 写视图 + 即时 revoke + forward 开关）

24. 浏览器顶栏「生成 enrollment token」按钮 → 调 `POST /api/admin/enroll/tokens` 弹窗显示新 token（一次性）
25. 「+ 发布服务」对话框选设备 → 提交 `POST /api/admin/services` → 表格立刻刷新
26. 「+ 添加 forward」对话框 owner 设备 × 远端服务 笛卡尔积选择 → 提交 `POST /api/admin/forwards`
27. service/forward 行内「停用/启用/删除」按钮 → 对应 admin POST/DELETE → daemon 在 reload 周期内自动调隧道
28. device 行的「撤销」按钮 → `POST /api/admin/devices/{id}/revoke`：DB 删 + `tunnel.RemoveDevice` + daemon 收到 401 自杀 → 公网端口立刻不可达，不需要重启 server
29. 全部端点配套 `omlctl forward enable/disable` CLI 命令；写操作和原 device API 走两条路径但语义一致

### M5.2.Y 增量（Tauri 桌面壳）

30. `tauri/src-tauri/` Rust 2.x 项目；`make tauri-dev` cargo run / `make tauri-build` 出 .app/.exe/AppImage
31. `make tauri-sync` 把 `internal/server/web/{index.html,style.css,app.js}` 拷到 `tauri/dist/`，单一来源
32. `app.js` 通过 `window.__TAURI_INTERNALS__` 自适应：浏览器同源 / Tauri 跨源（前缀 server URL）
33. Tauri 启动时若无 server URL 先进入「连接服务器」视图，存 localStorage 后进入登录页
34. 主界面 Tauri 环境多一个「本机」tab：填 omlctl 路径 + config 后启停 daemon；状态徽章实时反映
35. omlserver 给 /api/* 加 CORS headers（Allow-Origin: *），让 Tauri webview 跨源 fetch 不被拒
36. Rust IPC 命令 `daemon_start/stop/status`：Unix 用 SIGTERM + 3s 等优雅退出，超时再 SIGKILL；状态用 `Child::try_wait` 检测

### P+ 增量（账号密码登录）

44. migration 0005 加 `admin_users` 和 `sessions` 两表
45. `internal/auth/password.go` 提供 argon2id `HashPassword/VerifyPassword`（无外部 crypto 依赖）+ `NewSessionToken` 生成 `sess_xxx`
46. `POST /api/auth/login` 校验密码后签发 session（7 天 TTL），返回明文 token + 用户信息一次；`POST /api/auth/logout` 删 session；`GET /api/auth/me` 返回当前登录用户
47. `authAdminMiddleware` 改造为双轨：先试 session，再试 admin_token；通过 context 把 actor 字符串注入（user:alice / admin:hash），audit 直接读 context
48. 后台 `runSessionReaper` 每小时清过期 session；启动时立刻跑一次
49. CLI `omlserver admin user set/list/delete`：set 支持 `--password` 或从 stdin 读一行
50. 前端登录页改用户名+密码两字段；登录后 session token 仍存 localStorage（同 admin token 形态），fetch Authorization Bearer 自动带；登出调 `/api/auth/logout`
51. admin_token 路径完整保留：CI/curl 监控等机器对机器场景不需要改

### P1+P2+P3 增量（健康加固）

37. `internal/server/` 从 4 文件拆为 10 文件，最大文件 481 行；router / lifecycle / helpers / handlers / middleware 各司其职
38. `store.ListServicesJoined` / `ListForwardsJoined` / `ListDevicesWithCounts` 三个 JOIN 替代 N+1：admin/list 端点从 O(n×m) 退化为 O(1) SQL
39. `store.IsUniqueViolation(err)` 用 `errors.As(*sqlite.Error)` 类型安全识别 UNIQUE/PRIMARY KEY 冲突；fallback 仍保留字符串匹配防驱动 API 变更
40. Tauri Rust 新增 `daemon.rs` 把 spawn 逻辑独立成 `DaemonManager`，配 4 个 happy/edge-path 单测（用 `/bin/sleep` 当 fake daemon）
41. migration 0004 加 `audit_log` 表；`server.audit(ctx, actor, action, target, detail)` 在 10 个关键 handler 调用点写入；actor 用 `admin:<token_hash_short>` 不泄漏明文
42. `GET /api/admin/metrics` 一次 SQL 拿全设备/服务/forward/token/端口池/uptime 计数；`GET /api/admin/audit?limit=N` 倒序拉审计
43. Web UI 加「审计」tab 列最近 200 条；服务端信息页新增 metrics 网格（设备/服务/forward 计数 + 端口池利用率 + uptime）
