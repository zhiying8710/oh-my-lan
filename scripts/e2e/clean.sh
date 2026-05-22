#!/usr/bin/env bash
# 清掉 e2e 留的所有进程 + 工作目录
set -e

WORK="/tmp/oml-e2e"

# 杀掉相关进程（按 pidfile + 通用 fallback）
for f in "$WORK"/*/daemon.pid; do
  [[ -f "$f" ]] && kill "$(cat "$f")" 2>/dev/null || true
done
pkill -f "omlserver --config $WORK" 2>/dev/null || true
pkill -f "omlctl --config $WORK" 2>/dev/null || true
pkill -f 'TCPServer' 2>/dev/null || true   # python echo server

# 等一下让进程退干净
sleep 0.5
rm -rf "$WORK"
echo "[e2e] cleaned $WORK"
