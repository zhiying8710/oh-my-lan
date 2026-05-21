# oh-my-lan 桌面客户端

基于 Tauri 2.x 的极简桌面壳。功能：

- 加载 Web Admin UI（与 `omlserver` 嵌入的同一份资源同源）
- 通过 IPC 调用本机 `omlctl` 启停 daemon 进程
- 默认无系统托盘、无自启、无通知；只有一个窗口

## 开发

需要 Rust toolchain（1.77+）。macOS WebView 用系统 WKWebView，无额外依赖；Linux 需要 `webkit2gtk-4.1`。

```bash
# 第一次：安装 Tauri CLI（仅打包时需要；cargo run 不需要）
cargo install tauri-cli --version '^2'

# 开发运行（自动 cargo run）
make tauri-dev

# 打包成 .app / .exe / AppImage
make tauri-build
# 产物在 tauri/src-tauri/target/release/bundle/
```

## 静态资源同源

`internal/server/web/` 是单一来源，`make tauri-sync` 会复制到 `tauri/dist/`。
`tauri-dev` 和 `tauri-build` 都会自动先 sync。

如果你只是改 web UI、不改 Rust 部分：直接 `make tauri-sync` 后重启 Tauri 窗口（dev 模式自动 reload）。

## macOS Gatekeeper（不签名）

第一次双击未签名的 `.app` 会被警告"无法打开因为来自未识别开发者"。处理方式：

1. 在 Finder 找到 `.app`
2. **Ctrl + 单击** → 选「打开」
3. 弹窗里点「打开」
4. 之后双击即可正常启动

或者运行：
```bash
xattr -dr com.apple.quarantine /path/to/oh-my-lan.app
```

## 工作流

1. 启动 Tauri 应用
2. 第一次会问 server URL（例如 `http://vps:8080`）
3. 输入 admin token 登录
4. 主界面跟浏览器 Web Admin 一样，外加一个「本机」tab：
   - 填 omlctl 路径（例如 `/usr/local/bin/omlctl`）
   - 填 config 路径（例如 `~/.config/oml/client.yaml`）
   - 启动 / 停止 / 刷新 按钮

## IPC API（仅供前端调用，外部不暴露）

| Command | 参数 | 返回 | 说明 |
| --- | --- | --- | --- |
| `daemon_start` | `ctlPath`, `configPath` | `u32` pid | spawn 子进程；若已在跑则返回错误 |
| `daemon_stop`  | — | — | Unix: SIGTERM + 最长 3s 等优雅退出；超时则 SIGKILL |
| `daemon_status` | — | `{running, pid}` | try_wait 检查子进程状态 |

## 已知限制

- daemon 子进程的 stdout/stderr 在 Tauri 这一层是 `null`。要看 daemon 日志请：
  - 直接 `omlctl ... daemon start` 跑在终端
  - 或让 systemd/launchd 接管，日志走 journalctl/log show
- Tauri 进程退出**不会**杀掉它启动过的 daemon 子进程（OS 层进程是独立的）
- 不签名意味着 macOS 首次启动需手动放行（见上文 Gatekeeper 章节）
