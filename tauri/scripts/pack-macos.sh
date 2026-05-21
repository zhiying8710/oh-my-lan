#!/usr/bin/env bash
#
# pack-macos.sh — 完整 macOS pack 流水线：
#   ① cargo tauri build --bundles app
#   ② 给 .app 注入 ATS 例外（允许明文 HTTP）
#   ③ 用 bundle_dmg.sh 在干净 staging dir 里重打 DMG
#
# 之所以拆三步：Tauri 的 `cargo tauri build --bundles app,dmg` 在 .app 和 .dmg
# 之间没有 hook，没法插入 plist 改动。这个脚本绕开这个限制。

set -euo pipefail

cd "$(dirname "$0")/../src-tauri"
ROOT="$(pwd)/../.."

echo "==> 1) build .app"
cargo tauri build --bundles app

APP="target/release/bundle/macos/oh-my-lan.app"
[[ -d "$APP" ]] || { echo "❌ 未找到 $APP"; exit 1; }

echo
echo "==> 2) 注入 ATS"
"$ROOT/tauri/scripts/inject-ats.sh" "$APP"

echo
echo "==> 3) 用 staging dir 重打 DMG"
STAGE=$(mktemp -d)
trap "rm -rf '$STAGE'" EXIT
cp -a "$APP" "$STAGE/"

DMG="target/release/bundle/dmg/oh-my-lan_0.1.0_$(uname -m | sed 's/x86_64/x64/; s/arm64/aarch64/').dmg"
mkdir -p "$(dirname "$DMG")"
rm -f "$DMG"

# bundle_dmg.sh 在 Tauri build 后存在；首次 build 时它由 tauri 自己拉
SCRIPT="target/release/bundle/dmg/bundle_dmg.sh"
if [[ ! -f "$SCRIPT" ]]; then
  # 没有就触发一次 tauri build --bundles dmg 让它写下来
  echo "  (首次：拉 bundle_dmg.sh)"
  cargo tauri build --bundles dmg 2>&1 | tail -3 || true
fi

bash "$SCRIPT" \
  --volname "oh-my-lan" \
  --icon "oh-my-lan.app" 180 170 \
  --app-drop-link 480 170 \
  --window-pos 200 120 \
  --window-size 660 400 \
  --hide-extension "oh-my-lan.app" \
  --icon-size 128 \
  "$DMG" "$STAGE" | tail -3

echo
echo "✓ 完成: $(pwd)/$DMG"
ls -lh "$DMG"
