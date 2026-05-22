# oh-my-lan

![oh-my-lan](./docs/branding/horizontal.png)

把你的多台设备（家里的 NAS、工作笔记本、出差用的小本、树莓派 …）
通过一台公网 VPS 串成一张 **个人 mesh**：

- 任意一台设备发布本地服务（SSH、内网 Web、文件共享、UDP DNS…），
  通过 VPS 公网端口暴露
- 任意一台设备能把别人的服务**映射成本地端口**直接连，不依赖公网 IP、UPnP、第三方账号
- 一份单二进制 server（VPS）+ 一份单二进制 client（每台设备），全部静态编译，零运行时依赖
- 配套桌面客户端（macOS / Windows）和 Web 管理界面，登录用账号密码

底层 TCP/UDP 隧道用 [chisel](https://github.com/jpillora/chisel)，控制面、设备注册、
mesh 路由、推送告警都在 oh-my-lan 自己实现。

## 适合你吗

- 个人 / 小团队规模（< 10 台设备）
- 有一台公网 VPS 作为中继
- 不想给每台机器配公网端口转发、不想注册第三方 VPN 账号
- 偏好命令行 + 单文件二进制 + SQLite 存储

不适合：高并发场景、多租户 SaaS、生产关键链路。

## 下载

[GitHub Release](https://github.com/zhiying8710/oh-my-lan/releases) 提供预编译包：

| 平台 | CLI（omlserver + omlctl） | 桌面客户端 |
|---|---|---|
| Linux amd64 / arm64 | `.tar.gz` | （用 CLI 即可，无桌面包） |
| macOS Intel / Apple Silicon | `.tar.gz` | `.dmg`（universal） |
| Windows x64 | `.zip` | `.msi` / `-setup.exe` |

CLI 包解压后两个二进制可以直接跑；桌面包按平台常规方式安装。

## 编译

```bash
git clone https://github.com/zhiying8710/oh-my-lan.git
cd oh-my-lan
make build               # 产 bin/omlserver + bin/omlctl
make tauri-build         # 可选：打桌面客户端（需 Rust + cargo-tauri）
```

依赖：

- Go 1.25+（静态编译，单文件输出）
- 桌面客户端额外需要：Rust 1.77+、`cargo install tauri-cli --version '^2'`，Linux 还需 `libwebkit2gtk-4.1`

## 安装

### 服务端（VPS）

1. 把 `omlserver` 二进制放到 `/opt/oml/`（或别处）
2. 拷一份配置：
   ```bash
   cp configs/server.example.yaml /etc/oml/server.yaml
   ```
   按注释把 `chisel_advertise_addr` 改成你的 VPS 公网地址，
   `port_pool` 改成想暴露的公网端口范围（防火墙也要放开）。
3. 跑：
   ```bash
   /opt/oml/omlserver --config /etc/oml/server.yaml
   ```
   推荐做成 systemd service 自启。

4. 一次性创建 admin 账号：
   ```bash
   /opt/oml/omlserver --config /etc/oml/server.yaml \
       admin user set --username alice --password 'YourPassword'
   ```

### 客户端（每台设备）

**桌面端（macOS / Windows）**：装 `.dmg` / `.msi`，打开后按界面引导：

1. 输入服务器地址 + 用户名 + 密码登录
2. 「本机」tab 里点「+ 生成 enrollment token」获取一次性 token
3. 同一界面填设备名 + 粘 token → 自动注册并启动 daemon
4. 想开机自启就点「开启自启」（macOS launchd / Windows VBS / Linux systemd-user 自动配好）

**命令行（Linux 推荐，macOS / Windows CLI 用户也可）**：

```bash
# 注册（从服务端 Web 拿一次性 token）
./omlctl enroll --server http://vps.example.com:8080 \
    --token ot_xxx --name my-laptop

# 启动 daemon，常驻
./omlctl daemon start --pid-file /var/run/oml.pid --log-file /var/log/oml.log

# Linux：systemd user unit 实现开机自启
# 写 ~/.config/systemd/user/oml-daemon.service，ExecStart 用上面命令；
# 然后 systemctl --user enable --now oml-daemon
```

## 使用

### 发布服务（把本机的端口暴露到公网）

```bash
# CLI
./omlctl service add --name ssh --proto tcp --local 127.0.0.1:22
# 服务端会从 port_pool 自动分配一个公网端口，比如 41001
# 之后任何人能 ssh 到 vps.example.com:41001 → 你的本机 22
```

桌面端：「服务」tab → `+ 发布服务` → 选协议、填本地地址（仅端口号默认 127.0.0.1）。

### 跨设备访问（mesh forward）

设备 B 已发布 ssh 服务，设备 A 想 `ssh 127.0.0.1:8022` 接通 B：

```bash
# A 上：先看 mesh 中其它设备已发布的服务
./omlctl service list --discover

# A 添加 forward：本机 8022 → B 的 ssh
./omlctl forward add --service <service-id> --local 8022

# 等下一轮 reload（默认 30s）
ssh -p 8022 user@127.0.0.1   # 流量经 chisel 加密中转到 B
```

桌面端：「Forward」tab → `+ 添加 forward` → 选目标服务、填本机映射端口。

### Web 管理

浏览器访问 `http://<vps>:8080/admin/`，用 admin 账号登录。
能看到设备列表 / 服务 / forward / 服务端运行指标 / 审计日志，
也能直接在 UI 上代设备发布服务 / 添加 forward / 撤销设备。

### 设备离线推送（可选）

「服务端」tab 内开启 bark 推送，填你的 [Bark](https://github.com/Finb/Bark) URL，
设备掉线超过阈值时自动 push 到你的 iOS / macOS。
点「测试推送」可立即验证 URL 可达。

### 撤销设备

```bash
# Web Admin 上点「撤销」（推荐，立刻失效）
# 或 CLI：
./omlserver --config /etc/oml/server.yaml device revoke <device-id>
```

设备 daemon 收到 401 后自杀退出；该设备的服务、forward 全部连带删除，
占用的公网端口归还 port_pool。

## 进一步阅读

- 架构总览、设计取舍、已知限制：[docs/architecture.md](docs/architecture.md)
- 扩展评估（网络拓扑 / 流量统计 / 远程日志）：[docs/extensions-eval.md](docs/extensions-eval.md)
- 设计语言、视觉规范：[DESIGN.md](DESIGN.md)、[docs/branding/README.md](docs/branding/README.md)
- 桌面客户端：[tauri/README.md](tauri/README.md)

## License

MIT.
