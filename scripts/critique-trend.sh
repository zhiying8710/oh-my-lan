#!/usr/bin/env bash
#
# critique-trend.sh — 在终端里画 .impeccable/critique/*.md 的 score 走势。
#
# 用法:
#   ./scripts/critique-trend.sh            # 全部 target，每个一段走势
#   ./scripts/critique-trend.sh <slug>     # 单个 target
#
# 每份 critique 报告头部都是 YAML frontmatter，由 /impeccable critique 写入：
#   ---
#   target: internal/server/web/index.html
#   total_score: 27
#   p0_count: 1
#   p1_count: 1
#   timestamp: 2026-05-21T06-33-00Z
#   slug: internal-server-web-index-html
#   ---
#
# 本脚本：按 slug 分组，按 timestamp 排序，输出表格 + ASCII 走势条。
# 0-40 满分映射到 20 字符宽的 progress bar，便于一眼对比迭代效果。

set -euo pipefail

DIR="$(cd "$(dirname "$0")/.." && pwd)/.impeccable/critique"
[[ -d "$DIR" ]] || { echo "未找到 $DIR；先跑 /impeccable critique 至少一次"; exit 1; }

want_slug="${1:-}"

# frontmatter parser：每个 md 提取 target/total_score/timestamp/slug，输出 4 字段 TSV。
# 用 sed 抽 frontmatter 块（两个 --- 之间），再 grep 每个 key——比 awk 的 array capture
# 跨平台（BSD awk 不支持 match 第三参数）。
extract() {
  local f="$1"
  local fm
  fm="$(sed -n '/^---$/,/^---$/p' "$f" | sed '1d;$d')"
  local target score ts slug
  target=$(printf '%s\n' "$fm"      | sed -n 's/^target: *//p'       | head -1)
  score=$(printf '%s\n' "$fm"       | sed -n 's/^total_score: *//p'  | head -1)
  ts=$(printf '%s\n' "$fm"          | sed -n 's/^timestamp: *//p'    | head -1)
  slug=$(printf '%s\n' "$fm"        | sed -n 's/^slug: *//p'         | head -1)
  printf '%s\t%s\t%s\t%s\n' "$slug" "$ts" "$score" "$target"
}

# 收齐所有 snapshot
tmp=$(mktemp)
trap "rm -f $tmp" EXIT
for f in "$DIR"/*.md; do
  [[ -e "$f" ]] || continue
  extract "$f" >> "$tmp"
done

# 按 slug + timestamp 排序
sorted=$(sort -t $'\t' -k1,1 -k2,2 "$tmp")

# 渲染：每个 slug 一组，列出每次 snapshot 的 score + ASCII bar
current_slug=""
while IFS=$'\t' read -r slug ts score target; do
  [[ -z "$slug" ]] && continue
  [[ -n "$want_slug" && "$slug" != "$want_slug" ]] && continue
  if [[ "$slug" != "$current_slug" ]]; then
    echo
    echo "═══ ${target:-$slug}"
    printf "  %-22s  %-6s  %s\n" "TIMESTAMP" "SCORE" "GRADE"
    current_slug="$slug"
  fi
  # 画 0-40 → 20 字符 bar
  filled=$(( score / 2 ))
  empty=$(( 20 - filled ))
  bar="$(printf '█%.0s' $(seq 1 $filled 2>/dev/null))$(printf '░%.0s' $(seq 1 $empty 2>/dev/null))"
  # 评级带：< 20 weak, 20-29 mid, 30-39 strong, 40 perfect
  grade="weak"
  (( score >= 20 )) && grade="mid"
  (( score >= 30 )) && grade="strong"
  (( score >= 40 )) && grade="perfect"
  printf "  %-22s  %4s/40  %s  %s\n" "$ts" "$score" "$bar" "$grade"
done <<< "$sorted"

echo
echo "（每 run /impeccable critique <target> 后这里会自动多一条记录。）"
