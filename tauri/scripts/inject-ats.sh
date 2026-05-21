#!/usr/bin/env bash
#
# inject-ats.sh — 给 .app/Contents/Info.plist 注入 NSAppTransportSecurity 例外，
# 允许 WKWebView 从 app 内部 fetch 明文 HTTP。
#
# 当前 oh-my-lan 服务端按 M0 决策走纯 HTTP，因此 Tauri webview 默认会被 ATS 拦截。
# 此脚本作为 post-build 步骤把这条限制关掉。等服务端升级到 HTTPS 后可以撤掉。
#
# 用法：./inject-ats.sh <path/to/Your.app>

set -euo pipefail

APP="${1:?usage: inject-ats.sh <path/to/App.bundle>}"
INFO="$APP/Contents/Info.plist"

if [[ ! -f "$INFO" ]]; then
  echo "[ATS] ✗ 没找到 Info.plist：$INFO" >&2
  exit 1
fi

# PlistBuddy add 已存在的 key 会报错；先无条件删，再添加，保证幂等
/usr/libexec/PlistBuddy -c "Delete :NSAppTransportSecurity" "$INFO" 2>/dev/null || true
/usr/libexec/PlistBuddy -c "Add :NSAppTransportSecurity dict" "$INFO"
/usr/libexec/PlistBuddy -c "Add :NSAppTransportSecurity:NSAllowsArbitraryLoads bool true" "$INFO"

echo "[ATS] ✓ 已往 $INFO 注入 NSAllowsArbitraryLoads=true"
