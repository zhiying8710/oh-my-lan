#!/usr/bin/env bash
#
# e2e/run.sh — 单机多端口 mesh smoke
#
# 步骤详见 scripts/e2e/README.md。失败时不自动清理，留现场便于调试；
# 完成后用 ./scripts/e2e/clean.sh 清。

set -e

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
WORK="/tmp/oml-e2e"

# 端口规划：避开常用、保持人读友好
SERVER_HTTP_PORT=18080
SERVER_CHISEL_PORT=18443
PORT_POOL_MIN=19000
PORT_POOL_MAX=19999
# A 上准备一个 echo TCP 监听给 service 用
ECHO_PORT=19100
# B 上 forward A 的 echo 映射到本机的这个端口
B_FORWARD_PORT=19200

# 工具函数
log()  { printf "[e2e] %s\n" "$*"; }
fail() { printf "[e2e] ✗ %s\n" "$*" >&2; exit 1; }

# 0. 构建
log "0/9 编译 omlserver + omlctl"
cd "$ROOT"
make build > /tmp/oml-e2e-build.log 2>&1 || { cat /tmp/oml-e2e-build.log; fail "make build"; }

# 准备工作目录
rm -rf "$WORK"
mkdir -p "$WORK/server" "$WORK/A" "$WORK/B"

# 1. server config + start
log "1/9 启动 omlserver（HTTP :$SERVER_HTTP_PORT, chisel :$SERVER_CHISEL_PORT）"
cat > "$WORK/server/server.yaml" <<EOF
listen_addr: ":$SERVER_HTTP_PORT"
chisel_listen_addr: ":$SERVER_CHISEL_PORT"
chisel_advertise_addr: "127.0.0.1:$SERVER_CHISEL_PORT"
chisel_key_seed: "e2e-test-seed-never-use-in-production"
data_dir: "$WORK/server/data"
port_pool:
  min: $PORT_POOL_MIN
  max: $PORT_POOL_MAX
log:
  level: "info"
  format: "text"
# e2e 不在真 VPS 上跑——禁掉 VPS 账号自动管理（生产 must false）
disable_ssh_acct: true
EOF
"$ROOT/bin/omlserver" --config "$WORK/server/server.yaml" > "$WORK/server.log" 2>&1 &
SERVER_PID=$!

# 等 server HTTP 就绪
for i in $(seq 1 30); do
  if curl -sf "http://127.0.0.1:$SERVER_HTTP_PORT/healthz" >/dev/null; then
    break
  fi
  sleep 0.5
done
curl -sf "http://127.0.0.1:$SERVER_HTTP_PORT/healthz" >/dev/null || fail "server HTTP 未就绪；看 $WORK/server.log"

# 2. 创建 admin + 拿 token
log "2/9 创建 admin 用户 + token"
"$ROOT/bin/omlserver" --config "$WORK/server/server.yaml" admin user set --username e2e --password e2e-pass 2>&1 | tail -3
ADMIN_TOKEN=$("$ROOT/bin/omlserver" --config "$WORK/server/server.yaml" admin token create --label e2e 2>&1 | grep -oE 'oat_[A-Za-z0-9_-]+' | head -1)
[[ -n "$ADMIN_TOKEN" ]] || fail "拿不到 admin token"

# 3. 生成 enrollment token
log "3/9 生成 enrollment token"
ENROLL_JSON=$(curl -sf -X POST \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  "http://127.0.0.1:$SERVER_HTTP_PORT/api/admin/enroll/tokens")
ENROLL_TOKEN=$(echo "$ENROLL_JSON" | grep -oE 'ot_[A-Za-z0-9_-]+' | head -1)
[[ -n "$ENROLL_TOKEN" ]] || fail "拿不到 enroll token：$ENROLL_JSON"

# 4-5. 注册设备 A & B
for name in A B; do
  log "4-5/9 注册设备 $name"
  cat > "$WORK/$name/client.yaml" <<EOF
server_url: "http://127.0.0.1:$SERVER_HTTP_PORT"
device_name: "device-$name"
data_dir: "$WORK/$name/data"
reload_interval_seconds: 2
log:
  level: "info"
  format: "text"
EOF
  # A 用上面的 token，B 拿一个新的
  if [[ "$name" == "B" ]]; then
    ENROLL_TOKEN=$(curl -sf -X POST \
      -H "Authorization: Bearer $ADMIN_TOKEN" \
      "http://127.0.0.1:$SERVER_HTTP_PORT/api/admin/enroll/tokens" \
      | grep -oE 'ot_[A-Za-z0-9_-]+' | head -1)
  fi
  "$ROOT/bin/omlctl" --config "$WORK/$name/client.yaml" enroll \
    --server "http://127.0.0.1:$SERVER_HTTP_PORT" \
    --token "$ENROLL_TOKEN" \
    --name "device-$name" 2>&1 | tail -3 || fail "enroll $name 失败"

  # 启 daemon
  "$ROOT/bin/omlctl" --config "$WORK/$name/client.yaml" daemon start \
    --pid-file "$WORK/$name/daemon.pid" \
    --log-file "$WORK/$name/daemon.log" &
  sleep 0.3
done

# 等 daemon online
sleep 3

# 6. A 上起一个 echo TCP server，发布为 service
log "6/9 在 A 上起 echo TCP + 发布 service"
# Python 一行 echo server
python3 -c "
import socketserver
class H(socketserver.BaseRequestHandler):
    def handle(self):
        data = self.request.recv(4096)
        self.request.sendall(b'ECHO:'+data)
socketserver.TCPServer.allow_reuse_address = True
srv = socketserver.TCPServer(('127.0.0.1', $ECHO_PORT), H)
srv.serve_forever()
" >/dev/null 2>&1 &
ECHO_PID=$!
sleep 0.5

# 用 admin API 发布服务（避开 omlctl 自身 IPC，少一个变量）。
# 历史教训：早期用 grep+regex 提 device_id，admin /devices 返回单行 JSON 时 `grep -B1`
# 不起作用（没有"前一行"），会错误地拿到第一个设备的 id。改用 jq 按 name 精确过滤——
# CI ubuntu-latest 自带 jq，本地开发也都安装了。
DEVICES_JSON=$(curl -sf -H "Authorization: Bearer $ADMIN_TOKEN" "http://127.0.0.1:$SERVER_HTTP_PORT/api/admin/devices")
A_DEVICE_ID=$(echo "$DEVICES_JSON" | jq -r '.devices[] | select(.name=="device-A") | .id')
B_DEVICE_ID=$(echo "$DEVICES_JSON" | jq -r '.devices[] | select(.name=="device-B") | .id')
[[ -n "$A_DEVICE_ID" && -n "$B_DEVICE_ID" ]] || fail "拿不到 device id（A=$A_DEVICE_ID B=$B_DEVICE_ID）"

SVC_JSON=$(curl -sf -X POST \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"device_id\":\"$A_DEVICE_ID\",\"name\":\"echo\",\"protocol\":\"tcp\",\"local_addr\":\"127.0.0.1:$ECHO_PORT\"}" \
  "http://127.0.0.1:$SERVER_HTTP_PORT/api/admin/services") || fail "发布 service 失败"
SVC_ID=$(echo "$SVC_JSON" | jq -r '.id')
echo "  service id = $SVC_ID"

# 等 reload
sleep 3

# 7. B 上 forward A 的 echo
log "7/9 在 B 上添加 forward (owner=$B_DEVICE_ID)"
curl -sf -X POST \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"owner_device_id\":\"$B_DEVICE_ID\",\"remote_service_id\":\"$SVC_ID\",\"local_port\":$B_FORWARD_PORT}" \
  "http://127.0.0.1:$SERVER_HTTP_PORT/api/admin/forwards" > /dev/null || fail "添加 forward 失败"

# 等 B 的 daemon reload + chisel client 重连
sleep 4

# 8. 通过 B 的本地 forward 端口访问 A 的 echo service。
# 用 python 而非 nc：macOS 的 nc -w 行为与 Linux openbsd-nc 不一致（macOS 在 stdin EOF
# 后不会等待 server 响应就退出），脚本要跨平台稳定运行必须避开 nc。python3 我们已经依赖了
# （上面起 echo server），直接复用。
log "8/9 验证 mesh forward 联通"
RESP=$(python3 -c "
import socket, sys
s = socket.socket()
s.settimeout(5)
s.connect(('127.0.0.1', $B_FORWARD_PORT))
s.sendall(b'hello-mesh')
buf = s.recv(4096)
s.close()
sys.stdout.write(buf.decode('utf-8', errors='replace'))
")
[[ "$RESP" == "ECHO:hello-mesh" ]] || fail "forward 联通失败：得到 '$RESP'"
log "  ✓ B:$B_FORWARD_PORT → A:$ECHO_PORT → ECHO:hello-mesh"

# 9. 清理
log "9/9 cleanup"
kill $ECHO_PID 2>/dev/null || true
for f in "$WORK/A/daemon.pid" "$WORK/B/daemon.pid"; do
  if [[ -f "$f" ]]; then kill "$(cat "$f")" 2>/dev/null || true; fi
done
kill $SERVER_PID 2>/dev/null || true
sleep 0.5

log "═══ e2e smoke 全部通过 ✓"
log "保留现场：$WORK；用 ./scripts/e2e/clean.sh 清"
