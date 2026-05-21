# oh-my-lan

![oh-my-lan](./docs/branding/horizontal.png)

个人内网工具服务：基于 [chisel](https://github.com/jpillora/chisel) 的轻量隧道控制平面。让自己的多台设备之间安全互通，并把任一设备的本地 TCP/UDP 服务通过公网 VPS 暴露给指定访问者。

## 项目状态

当前 **M5.2.Y + P1/P2/P3 已完成**（功能完备 + 健康加固）：

- 服务端控制平面 HTTP API + 嵌入式 chisel server
- 一次性 token 设备注册；fingerprint 与 DB 0o600 持久
- 多 TCP / UDP 服务发布（每服务一公网端口，自动从端口池分配）
- daemon 运行中检测服务/forward 变更自动 reload
- device 在线状态流转 + 离线 reaper 自动降级
- mesh 客户端互访（TCP / UDP），DNS over UDP 验证通过
- **账号密码登录**：argon2id 哈希、session token（7 天 TTL）、过期自动清；Web/Tauri 输入用户名密码即可，无需手填 token
- admin_token 兼容保留：机器对机器场景（CI、监控）
- Web Admin UI（浏览器同源） + Tauri 桌面壳（IPC 启停本机 daemon），共享同一份 web 代码
- Observability：`/api/admin/metrics` + `/api/admin/audit` + UI 审计 tab，含 `auth.login/logout` 等 12 个关键操作
- 代码健康：server 包内部分层、SQL JOIN 消除 N+1、`IsUniqueViolation` 类型安全错误识别、Tauri Rust 单测
- CLI：`omlserver {admin user,admin token,device,token} ...`、`omlctl {enroll,service,forward,daemon} ...`

未实现：自动备份。详细规划与已知限制见 [docs/architecture.md](docs/architecture.md)。

## 快速开始

```bash
make build

# 服务端
./bin/omlserver --config configs/server.example.yaml

# 在服务端本机另开终端：生成一次性 token
./bin/omlserver token create --config configs/server.example.yaml

# 客户端注册（拿到上一步的 ot_xxx）
./bin/omlctl enroll \
  --server http://your-vps:8080 \
  --token ot_xxx \
  --name my-laptop

# 发布本地服务到公网
./bin/omlctl service add --name ssh --proto tcp --local 127.0.0.1:22

# 启动 daemon 维持隧道（前台运行）
./bin/omlctl daemon start
```

### Web Admin UI（推荐：账号密码登录）

```bash
# 一次性建账号（服务器本机执行）
./bin/omlserver admin user set --username admin --password "YourPassword" \
  --config configs/server.example.yaml
# 或从 stdin 读（避免密码进 shell history）：
# read -s PW && ./bin/omlserver admin user set --username admin --password "$PW" --config ...
```

浏览器访问 `http://<server>:8080/admin/`，输入用户名+密码登录。Session 默认 7 天 TTL，自动续期 last_used；服务端后台每小时清过期 session。

管理用户：`omlserver admin user list` / `omlserver admin user delete <username>`。

**机器对机器场景（CI / 监控抓 metrics）** 仍可使用长期 token：
```bash
./bin/omlserver admin token create --label "ci"
# admin token: oat_xxx...
curl -H "Authorization: Bearer oat_xxx..." http://server:8080/api/admin/metrics
```
所有 admin 端点同时接受 session token 与 admin token，行为完全一致。

### Tauri 桌面客户端（macOS / Windows）

GitHub Release 提供 macOS `.dmg`（universal binary：Apple Silicon + Intel）与 Windows `.msi` / `-setup.exe`。
**Linux 不发桌面包**——直接用 `omlctl` 命令行管理本机 daemon 即可（覆盖桌面客户端「本机」tab 的全部功能；
`omlctl daemon start --pid-file ...` 配合 systemd user unit 可达到与桌面 "开机自启" 等效行为）。

需要从源码构建（任何 OS 都行，但只有 macOS / Windows 是官方发布平台）：

```bash
# 一次性安装 Tauri CLI（仅打包时需要）
cargo install tauri-cli --version '^2'

# 开发运行（dev 模式）
make tauri-dev

# 打包成 .app / .exe
make tauri-build
# 产物在 tauri/src-tauri/target/release/bundle/
```

桌面客户端比浏览器多了一个「本机」tab，可以直接启动/停止本机 daemon 子进程。详见 [tauri/README.md](tauri/README.md)。

### mesh：访问另一台设备的服务

设备 B 已经发布了 ssh 服务，设备 A 想直接 `ssh 127.0.0.1:8022` 接通 B：

```bash
# A 上：先看 B 的服务，拿 service id
./bin/omlctl service list --all
# ID                                DEVICE  NAME  PROTO  PUBLIC  ENABLED
# abcd...                           B       ssh   tcp    41001   true

# A 创建 forward：本机 8022 → B 的 ssh
./bin/omlctl forward add --service abcd... --local 8022

# 等 daemon reload（默认 30s，可改 reload_interval_seconds）
ssh -p 8022 user@127.0.0.1   # 流量经 chisel 加密中转
```

## 里程碑

| 里程碑 | 内容 |
| --- | --- |
| M0 ✅ | 仓库骨架与 CI |
| M1 ✅ | 控制平面 + enrollment + 单服务 TCP 发布 |
| M2 ✅ | 多服务、热 reload、device 状态、CLI 全量 |
| M3 ✅ | mesh 客户端互访（TCP） |
| M4 ✅ | UDP 支持验证 |
| M5.1 ✅ | Web Admin UI 只读（embed.FS，零构建） |
| M5.2.X ✅ | Web Admin 写视图 + device revoke 即时生效 |
| M5.2.Y ✅ | Tauri 桌面壳 |
| P1+P2+P3 ✅ | 健康加固：server 分层、N+1 修复、错误码封装、Tauri 单测、metrics + audit |
| P+ ✅ | 账号密码登录 + session（替代手工填 token） |
| M6 | 自动备份 / DB 维护 |
| M6 | 观测性、审计日志、自动备份 |
