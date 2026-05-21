---
target: internal/server/web/index.html
total_score: 27
p0_count: 1
p1_count: 1
timestamp: 2026-05-21T06-33-00Z
slug: internal-server-web-index-html
---
## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|---|---|---|
| 1 | Visibility of System Status | 3 | Tab/badge 状态明确，但表格加载期间无 stale 指示 |
| 2 | Match System / Real World | 4 | 术语保留原文，符合 deadpan tooling |
| 3 | User Control and Freedom | 2 | 撤销/删除走原生 confirm()，无 undo |
| 4 | Consistency and Standards | 3 | ✓/✗ vs 文本 / count 单元格 mono 不一致 |
| 5 | Error Prevention | 2 | 撤销设备影响面无分级 |
| 6 | Recognition Rather Than Recall | 3 | forwards 表 → 单成一列；devices tab 无加设备主操作 |
| 7 | Flexibility and Efficiency | 2 | 无键盘快捷键 |
| 8 | Aesthetic and Minimalist Design | 4 | Quiet Switchboard 落地 |
| 9 | Error Recovery | 3 | showAlert 全文 stderr 优秀；401 裸露 |
| 10 | Help and Documentation | 1 | 全 UI 无 tooltip / 字段说明 |
| Total | | 27/40 | Mid-strong |

## Anti-Patterns Verdict

不像 AI 生成的。已经主动避开大多数 slop 套路。真实命中：
- #fff/#000 硬编码 (style.css:73, 216)
- Modal-as-first-thought 反向命中：危险操作该用 modal 反而走原生 confirm()

检测器 false positive：low-contrast 全部是 dark-mode token 错配 light-mode bg；overused-font 命中的是 system stack 的 fallback；flat-type-hierarchy 是 16-Cap Rule 主动选择。

## Priority Issues

### [P0] revoke-device / delete-* 还在用浏览器原生 confirm()
违反 Native Dialog Rule；唯一可能造成数据丢失的漏洞。Fix: app.js:326-328 改用 showConfirm()。
Command: /impeccable harden

### [P1] daemon-card 6 按钮 + 双 primary
违反 One-Primary-Per-Card + 认知负荷上限。Fix: 拆两张 local-card（daemon 生命周期 / autostart）。
Command: /impeccable layout

### [P2] 空状态 + 加载状态缺失
首次用户卡在 devices 空表；老用户弱网下 stale 数据无提示。Fix: 空态加引导链 + table aria-busy + muted spinner。
Command: /impeccable onboard + /impeccable clarify

### [P3] Triple-Code Rule 实际只是 Double-Code
.status-offline 没字重；statusBadge() 只两轴。Fix: [●]/[○] 字符前缀引入第三轴 + 显式声明 weight。
Command: /impeccable harden

### [P4] devices tab 无主操作 + forwards 表 → 单成列
违反 Recognition over Recall + Aesthetic minimalist。Fix: devices.panel-actions 加生成 token 按钮；forwards 合并箭头列。
Command: /impeccable distill

## Persona Red Flags

Alex (Power User): 无快捷键 / Esc 取消行为微妙不一致 / 必须鼠标点刷新
Jordan (First-Timer): 空表无引导 / 本机 tab 同屏 6 按钮认知压力 / 401 错误裸露

## Minor Observations
- ✓/✗ → 已启用/已停用
- tabs gap 4px → 8px
- .card max-width 隐式覆盖，应显式 .card.narrow / .card.wide
- DESIGN.md 第 4 条 Design Principle 应该有 CI lint (grep confirm()
- Audit tab in-context help 模式应推广
