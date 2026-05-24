# 安全：通过 SSH 跳板访问 service

## 背景：5/21 mini-pc 勒索事故

5/21 在 admin UI 上把 mini-pc 的 RDP(3389) / SSH(22) 通过 oml 暴露到
`47.94.226.62:40000/40001`（chisel R-listener bind `0.0.0.0`）。3 天暴露窗口内，
攻击者通过自动化扫描 + RDP 暴破入侵 mini-pc，部署勒索软件。

事后核实 VPS 本身未被攻陷——oml 严格按照 admin 指令完成了"暴露"动作。问题在于
**"把 RDP/SSH 这类协议层鉴权弱的服务直接暴露到公网"** 本身就是受害模式，无论中间
工具是 nginx / oml / frp / 路由器 port forward 都一样。

## 现在的设计

```
                  (公网客户端)
                        │
                  TCP :40000  ← 现在 listener bind 127.0.0.1，公网扫不到
                        ✗ connection refused
                        │
              ┌─────────────────────┐
              │  VPS  47.94.226.62  │
              │  sshd :22 公网       │ ← key-only for oml-* 账号
              │  ↓ port-forward     │
              │  ↓ 限定 127.0.0.1:40000-49999
              │  chisel R 127.0.0.1:40000 │
              │   ↓ 反向隧道         │
              └──────────┬──────────┘
                         │
              ┌──────────▼──────────┐
              │  设备 oml daemon     │
              │  → 127.0.0.1:3389   │
              └─────────────────────┘
```

合法访问路径：

```bash
# 在你本地（macbook、Windows、Linux 都一样）
ssh -i ~/.config/oml/ssh_key -N -L 13389:127.0.0.1:40000 oml-9871b2b5@47.94.226.62

# 然后 mstsc / rdp-client 连本机
mstsc /v:127.0.0.1:13389
```

## 设计原则

1. **bind_local 默认 true**：所有新发布 service 的 chisel R-listener 都绑 127.0.0.1。
   admin 必须显式 `bind_local: false` 才能 0.0.0.0 公网暴露（前端会大红警告）。
2. **SSH key 由 oml 自管**：每台 device enroll 时 omlctl 自动生成 ed25519 keypair
   存 `~/.config/oml/ssh_key`（macOS: `~/Library/Application Support/oh-my-lan/`，
   Win: `%APPDATA%\oh-my-lan\`），私钥**永远不离开客户端本机**。
3. **VPS 账号一对一**：每台 device 对应一个 `oml-<id8>` 受限账号，nologin shell，
   `restrict + port-forwarding + permitopen=127.0.0.1:<具体端口>` 三层限制。
4. **撤销立即生效**：revoke device 第一步就 `usermod -L` + 清 authorized_keys +
   `pkill -u` 强断现存 session。`ssh_locked_at` 时间戳触发 7 天后 cron `userdel -r` 真删。
5. **SSH 密码登录保留**：sshd `Match User oml-*` 段强制 oml-* 账号 key-only；其它
   账号（root 等）保持你原本的密码登录习惯——sshd 全局配置不动。

## 受影响 / 不受影响的使用场景

| 场景 | 前 | 后 |
|---|---|---|
| oml client A 通过 forward 访问 client B 的 service | 走 chisel 隧道直连 | 一样，**无变化** |
| 第三方工具（mstsc / putty / 浏览器）直接连 `VPS:40000` | 直连可用，**这就是被入侵的路径** | 改走 `ssh -L` 跳板 + 本机 localhost |
| 浏览器开 admin UI | 直接 `http://VPS:58080/admin/` | 一样，**无变化** |
| omlctl daemon 自己连 chisel server | 出站到 `VPS:58443` | 一样，**无变化** |

只有"非 oml 客户端的第三方工具想访问 service"这一个场景被换成 SSH 跳板。装了
oml 客户端的电脑日常使用 0 变化。

## systemd 单元配置陷阱（5/25 调试经验）

oml-server 跑在 systemd 下，需要 useradd/usermod/userdel 操作 `/etc/passwd` `/etc/shadow` `/home/oml-*`。
如果用以下"看起来更安全"的组合**会失败**：

```
ProtectSystem=strict    # ✗ /etc/ RO → useradd cannot lock /etc/passwd
ProtectSystem=full      # ✗ 同上，/etc/ 仍 RO
ProtectHome=true        # ✗ /home/ 不可见 → useradd -m 无法 mkdir /home/<user>
```

哪怕 `ReadWritePaths=/etc /home` 也不能解决——`ProtectSystem`/`ProtectHome` 优先级更高。

**正确组合**（见 `scripts/deploy/oml-server.service`）：

```
ProtectSystem=true                                  # /usr /boot 只读，/etc 仍 RW
ProtectHome=false                                   # /home 可见可写
ReadWritePaths=/var/lib/oml /etc/oml /etc /home    # 显式白名单兜底
```

诊断方法：在 oml-server 的 mount namespace 内 strace useradd：

```bash
PID=$(systemctl show -P MainPID oml-server)
nsenter -t $PID -m -p strace -e openat,flock /usr/sbin/useradd -m -s /usr/sbin/nologin oml-debug
# 看到 EROFS = ProtectSystem 太严；看到 /home 不存在 = ProtectHome=true
```

## VPS 端部署

```bash
# 1. 部署 sudoers（仅允许 oml- 前缀账号操作）
cp scripts/deploy/oml-server-sudoers /etc/sudoers.d/oml-server
chmod 440 /etc/sudoers.d/oml-server
visudo -c

# 2. 部署 sshd Match 段
cp scripts/deploy/oml-sshd-hardening.conf /etc/ssh/sshd_config.d/
sshd -t
systemctl reload ssh

# 3. 升级 omlserver binary
mv /tmp/omlserver.new /opt/oml/omlserver
chmod +x /opt/oml/omlserver
systemctl restart oml-server

# 4. （首次部署）清旧 device 数据
systemctl stop oml-server
rm /var/lib/oml/oml.db
systemctl start oml-server
omlserver --config /etc/oml/server.yaml admin user set --username admin --password '<new-pwd>'
```

## 客户端 enroll 流程

```bash
# omlctl 内部：
#   1. EnsureSSHKey（生成 ed25519 + 落盘 ssh_key/ssh_key.pub 0600）
#   2. POST /api/devices/enroll body={token, device_name, ssh_pubkey}
#   3. server 在 VPS 上 useradd oml-<id8> + 写 authorized_keys
#   4. enroll 响应里返回 ssh_username/host/port
#   5. omlctl 把 ssh 跳板命令打到屏幕

omlctl enroll --server http://vps:58080 --token ot_xxx --name my-laptop

# 输出
# ✓ 注册成功
#   device_id=9871b2b5...
#   ssh -i ~/.config/oml/ssh_key -p 22 oml-9871b2b5@47.94.226.62
#   （后续访问 service 用：ssh -i ... -N -L <本机端口>:127.0.0.1:<public_port> oml-9871b2b5@vps -p 22）
```

## 操作手册：怎么 RDP 到 mini-pc

```cmd
:: 1. enroll 这台机器（一次性，已注册跳过）
omlctl enroll --server http://47.94.226.62:58080 --token <从 admin UI 拿> --name my-laptop

:: 2. 后台起 ssh 跳板（自启脚本 / 任务计划程序设置开机跑）
ssh -i %APPDATA%\oh-my-lan\ssh_key -N -L 13389:127.0.0.1:40000 oml-9871b2b5@47.94.226.62

:: 3. mstsc 连本机
mstsc /v:127.0.0.1:13389
```

桌面客户端 UI 会显示"复制 SSH 跳板命令"按钮——点一下 clipboard 拿到完整命令。

## 残留攻击面 + 缓解

| 风险 | 残留 | 缓解 |
|---|---|---|
| sshd :22 暴露公网（root 密码登录开） | 仍存在 | fail2ban 限暴破频率（推荐装） |
| oml-server 进程被入侵 → 可调 sudo useradd | 限 oml- 前缀 sudoers 兜底 | 把 oml-server 改 dedicated 用户跑 + sudoers 全列具体命令 |
| chisel R 隧道 ws 层未 TLS | 设备间流量经 chisel ssh transport 加密；ws 外层 plain | wss + Let's Encrypt（未做） |
| 客户端私钥泄漏 | 受 `0600` + OS 用户级权限保护 | macOS Keychain / Windows Hello 集成（未做） |

## 后续工作

- [ ] Web UI 加 "SSH 跳板信息" 卡片（仅浏览器显示），列每台 device 的 username + 复制命令按钮
- [ ] omlctl 加 `omlctl tunnel <local_port>:<service_id>` 子命令封装 ssh -L
- [ ] 装 fail2ban + 配置 sshd jail
- [ ] sshd 端口从 22 换成非常用端口（如 2200）减少扫描噪音
