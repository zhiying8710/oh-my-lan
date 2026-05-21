---
name: oh-my-lan
description: Personal-scale chisel control plane with a dual-surface admin UI (browser + Tauri desktop).
colors:
  bg: "#fafafa"
  fg: "#1a1a1a"
  muted: "#6a737d"
  border: "#e0e0e0"
  accent: "#2b6cb0"
  accent-fg: "#ffffff"
  card-bg: "#ffffff"
  hover: "#f1f5f9"
  online: "#2f855a"
  offline: "#a0aec0"
  error: "#c53030"
  danger: "#c53030"
typography:
  body:
    fontFamily: "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: 1.5
  title:
    fontFamily: "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif"
    fontSize: "16px"
    fontWeight: 600
    lineHeight: 1.3
  label:
    fontFamily: "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif"
    fontSize: "12px"
    fontWeight: 600
    lineHeight: 1.4
    letterSpacing: "0.5px"
  mono:
    fontFamily: "ui-monospace, SFMono-Regular, 'SF Mono', Menlo, monospace"
    fontSize: "12px"
    fontWeight: 400
    lineHeight: 1.4
rounded:
  sm: "4px"
  md: "6px"
  lg: "8px"
  pill: "999px"
spacing:
  xs: "4px"
  sm: "8px"
  md: "12px"
  lg: "16px"
  xl: "20px"
  "2xl": "24px"
components:
  button-default:
    backgroundColor: "{colors.card-bg}"
    textColor: "{colors.fg}"
    rounded: "{rounded.sm}"
    padding: "6px 14px"
    typography: "{typography.label}"
  button-default-hover:
    backgroundColor: "{colors.hover}"
    textColor: "{colors.fg}"
  button-primary:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.accent-fg}"
    rounded: "{rounded.sm}"
    padding: "6px 14px"
  button-danger:
    backgroundColor: "{colors.card-bg}"
    textColor: "{colors.danger}"
    rounded: "{rounded.sm}"
    padding: "6px 14px"
  button-danger-hover:
    backgroundColor: "{colors.danger}"
    textColor: "{colors.accent-fg}"
  button-link:
    backgroundColor: "transparent"
    textColor: "{colors.muted}"
    rounded: "{rounded.sm}"
    padding: "6px 4px"
  button-disabled:
    backgroundColor: "{colors.bg}"
    textColor: "{colors.muted}"
    rounded: "{rounded.sm}"
    padding: "6px 14px"
  input-default:
    backgroundColor: "{colors.card-bg}"
    textColor: "{colors.fg}"
    rounded: "{rounded.sm}"
    padding: "8px 12px"
    typography: "{typography.body}"
  card:
    backgroundColor: "{colors.card-bg}"
    rounded: "{rounded.md}"
    padding: "24px"
  table:
    backgroundColor: "{colors.card-bg}"
    rounded: "{rounded.md}"
  status-badge:
    backgroundColor: "{colors.bg}"
    textColor: "{colors.fg}"
    rounded: "{rounded.pill}"
    padding: "4px 12px"
    typography: "{typography.label}"
  metric-card:
    backgroundColor: "{colors.card-bg}"
    rounded: "{rounded.md}"
    padding: "16px"
  dialog:
    backgroundColor: "{colors.card-bg}"
    textColor: "{colors.fg}"
    rounded: "{rounded.lg}"
    padding: "24px"
---

# Design System: oh-my-lan

## 1. Overview

**Creative North Star: "The Quiet Switchboard"**

oh-my-lan 的 UI 是一台**安静的接线总机**——它接住所有 mesh 里的设备/服务/forward 信号，按需点亮指示灯，绝不主动吸引注意力。它不庆祝、不感叹、不引导，因为操作员（也就是项目作者本人）只在需要的时候过来扫一眼，确认链路正常，加一条规则，然后关掉窗口。

系统的密度高、对比克制、动作迅捷。所有装饰性元素都被砍掉：没有大数字 metric、没有渐变 hero、没有彩色阴影、没有 emoji 状态徽章。技术实体（pid、port、device_id、tunnel_secret 短指纹）保持原文裸露，不被翻译或图标化。状态指示同时使用**文本 + 颜色 + 字重**三重编码：在线行 fg-emerald + "在线" + bold，离线行 fg-gray + "离线" + regular。颜色只是这套语言的一个轴，永远不是唯一通道。

明确拒绝的方向：紫渐变 SaaS dashboard、企业级 IT 厚重灰边框、玻璃拟态/彩色阴影/bounce 动画、过度留白下的"每点一下开 modal"。

**Key Characteristics:**

- **Dual-surface, single-codebase**：浏览器 `/admin/` 与 Tauri desktop 共用同一份 vanilla JS + CSS，视觉零分叉。
- **Native theme inheritance**：通过 `prefers-color-scheme` 浅/深双主题切换，跟随系统而不内置 toggle。
- **Tinted neutrals + one accent ≤10%**：Restrained 配色策略；蓝色 accent 只用在主按钮、当前 tab 下划线、currently-selected 状态。
- **Dense data, not airy whitespace**：表格行高 8px×2、padding 12px；信息密度优先于呼吸感。
- **Flat surfaces, bordered hierarchy**：层级靠边框 + 背景色阶表达，几乎不用 box-shadow。

## 2. Colors

Restrained 配色策略：色板由 tinted neutrals 与极少量功能语义色组成；蓝色 accent 占任意一屏 ≤10%。配色采用功能命名（不是设计专名），与 CSS 变量名一一对应，无映射成本。

### Primary

- **accent** (`#2b6cb0` light / `#60a5fa` dark)：主按钮背景、当前 tab 下划线、选中态指示。**每屏出现 ≤10%**。绝不用于装饰性面积块。

### Neutral

- **bg** (`#fafafa` / `#1a1a1a`)：页面底色，最低层。
- **card-bg** (`#ffffff` / `#232323`)：内容容器（card / table / dialog）。略浅于 bg（暗模式略亮于 bg），靠**纯色阶**表达"浮在面上"，不用阴影。
- **fg** (`#1a1a1a` / `#e4e4e7`)：主文字色。
- **muted** (`#6a737d` / `#9ca3af`)：次要文字（表头、label、placeholder、说明性 .muted 段落、未激活 tab）。
- **border** (`#e0e0e0` / `#2a2a2a`)：所有分割线、卡片边框、表格分行、输入框描边。**唯一的层级表达手段**。
- **hover** (`#f1f5f9` / `#2d2d2d`)：button hover 背景、table row hover 背景。在 bg/card-bg 之间插一档。

### Semantic (status colors)

- **online** (`#2f855a` / `#4ade80`)：设备在线、daemon 运行中、自启已开启。Pair with text "在线" / "运行中" / "已开启" + font-weight 600。
- **offline** (`#a0aec0` / `#6b7280`)：设备离线、daemon 已停止、自启未开启。**饱和度故意低**，"离线" 不该看起来像 error。
- **error / danger** (`#c53030` / `#f87171`)：实际故障、销毁性按钮 (revoke / delete)、关闭自启 confirm 弹窗的标题红。两个 token 同值，是历史遗留——未来合并成 `error` 即可，不要为了"漂亮"再加一支橘红或玫红。

### Named Rules

**The Triple-Code Rule.** 状态变化必须**同时**用颜色 + 文本 + 字重表达。"在线" 不能只靠 green dot——必须 green text + 加粗 + 字符 "在线"。这条规则的存在是因为色弱用户与 reduced-motion 用户不应被视觉单通道排除，也因为日志/截图常被复制到纯文本上下文。

**The Tinted Neutral Rule.** 所有中性色都向品牌蓝倾斜一个微小色温（实践中通过 bg/border 的灰里带一丝青蓝实现）。`#000` 和 `#fff` 永远不用。

**The Accent Quota Rule.** Accent 蓝在任意视图占 ≤10% 像素面积。currently-active tab 的下划线 + 当前 view 内主操作按钮 + 选中行高亮的总和不应超过这个预算。

## 3. Typography

**Body Font:** system stack — `-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif`。
**Mono Font:** system mono — `ui-monospace, SFMono-Regular, "SF Mono", Menlo, monospace`。
**Display Font:** 不存在。本系统不需要 display tier。

**Character:** 单家族系统字体 + 单 mono 两条声音线。系统字体跨平台跟随宿主（macOS 是 SF / Pingfang，Windows 是 Segoe UI / MSYH，Linux 是 Cantarell / Noto），零网络字体，零首帧闪烁。mono 字体专门承担**技术实体**：device_id、tunnel_secret 短指纹、IP:port、stderr 摘要、JSON 值。

### Hierarchy

- **Title** (16px / weight 600 / line-height 1.3)：`<h1>` 顶部 brand "oh-my-lan"，dialog 标题 `<h3>`，「本机 daemon」card 标题 `<h2>`。**整个 UI 最大字号就是 16px**——拒绝大字 metric。
- **Body** (14px / weight 400 / line-height 1.5)：默认正文、表格数据行、输入框文字、按钮文字。
- **Label** (12px / weight 600 / letter-spacing 0.5px / 通常 UPPERCASE)：表头 `<th>`、metric-card label、form label、status-badge 文本。tracked + uppercase 让 12px 字号也能"分量足"。
- **Mono** (12px / weight 400)：`<dd>` 定义列表的值、`.mono` 类、metric value、token-display block、`<code>` inline。
- **Muted note** (12px / muted color)：`.muted` 段落、placeholder、错误旁的辅助说明。

### Named Rules

**The 16-Cap Rule.** 任何文字 ≥ 18px 都是反模式。包括 dialog 标题、设备名、错误提示、空状态文案——一律 ≤ 16px。这条规则把整个系统的视觉重量压在密度而非字号上。

**The Mono For Technical Truth Rule.** pid、port、ID、fingerprint、JSON value、stderr——任何"机器生成的精确字符串"必须 mono。这让用户在视觉上立刻区分 "这是术语" vs "这是描述"。

**The No Display Font Rule.** 不引入第二个 sans 家族，不引入 serif，不引入 web font。系统字体的跨平台不一致是接受的代价。

## 4. Elevation

**纯平系统**。层级由 border + 背景色阶表达，几乎不用 box-shadow。这是"接线总机"调性的核心承诺：不通过虚拟光照模拟物理世界，因为操作员的桌面没有那种照明。

唯一的例外是 `<dialog>` 的浏览器原生 backdrop（`::backdrop { background: rgba(0,0,0,0.5) }`）——这不是阴影，是**焦点收束**：modal 期间把背后的 UI 全部压暗 50%，确保用户处理弹窗内容时不被分心。这个语义不可被装饰性 shadow 替代。

### Layer scale (by background only)

从最低到最高：

- **bg** (`#fafafa` / `#1a1a1a`)：页面底色。
- **card-bg** (`#ffffff` / `#232323`)：内容容器层。比 bg 浅一档（暗模式亮一档）。
- **hover** (`#f1f5f9` / `#2d2d2d`)：临时交互态。

所有"卡片浮在背景上"的感觉都靠 (card-bg vs bg) 的色阶差 + 1px border 同时表达，**不叠 shadow**。

### Named Rules

**The Flat-By-Default Rule.** 没有任何元素默认带 box-shadow。connector、tooltip、dropdown 之类未来如果出现，仍然走 border + 色阶；只有 native `<dialog>` 例外，且只用于焦点收束。

**The Border Is The Hierarchy Rule.** 删 border 会让结构崩溃。永远 1px、永远 `var(--border)`、永远完整四边——禁止 `border-left/right` 大于 1px 的彩色侧条。

## 5. Components

### Buttons

- **Shape**：圆角 4px (`rounded.sm`)。padding 6px × 14px（紧凑，配合 13px 字号）。绝不用 pill 圆角，那是 status-badge 的语义。
- **Default** (`button`)：`card-bg` 背景 + `border` 描边 + `fg` 文字。中性、最常见的次级操作。
- **Primary** (`.btn-primary` 或 `<button type="submit">`)：`accent` 背景 + `accent-fg` 文字。每个表单/卡片至多一个，承担"提交"语义。
- **Danger** (`.btn-danger`)：`card-bg` 背景 + `danger` 文字。hover 翻转成 `danger` 背景 + 白字。用于停止 daemon、撤销 token、关闭自启这类有副作用的动作。
- **Link** (`.btn-link`)：透明背景 + muted 文字 + underline。下沉重要度的"小动作"，比如「重新注册本机…」、「生成」附属于输入框的辅助按钮。
- **Disabled**：`opacity 0.4` + `cursor: not-allowed` + 强制 hover 无变化。覆盖所有 button 类（含 primary/danger/link）使禁用态在视觉上一致——不可点的按钮看起来就是"不可点"，不让用户走神去试。
- **Row button** (`.row-btn`)：12px 字号 + 3px × 8px padding，行内紧凑变体，专给表格行末的操作按钮（删除、enable/disable）。

### Inputs

- **Shape**：4px radius（与 button 同），padding 8px × 12px（比 button 略松，便于点击时不挤压文字）。
- **Default**：`card-bg` 背景 + `border` 描边 + `fg` 文字 + `width: 100%`。
- **Focus**：依赖浏览器原生 outline，不自定义 focus ring。Tauri WKWebView 与现代 Chrome/Firefox 的默认 outline 已经足够清晰。
- **Input with action**（`.input-with-action`）：flex 容器把输入框与右侧 `.btn-link` 紧贴排列；典型用例是「生成 token」按钮挂在 token 输入框右侧。

### Cards

- **Shape**：6px radius (`rounded.md`)，比 button 略大。
- **Background**：`card-bg`。
- **Border**：1px solid `border`，四边完整。
- **Padding**：24px (`spacing.2xl`)。
- **Variants**：登录/服务器配置卡 `max-width: 400px`、`margin: 40px auto`（屏幕居中的窄列）。`.local-card` 用于 Tauri「本机」tab 的 daemon 控制，`max-width: 720px`（更宽，承载表单 + 状态行）。

### Tables

- **Shape**：6px radius + `overflow: hidden` 让圆角生效。
- **Header (`<thead>`)**：背景 `bg`（比 card-bg 暗一档，与卡片整体形成"标签条"对比），`th` 用 label 字号（12px / 600 / tracked / uppercase）。
- **Rows**：`th`/`td` padding 8px × 12px。行间 1px `border` 分隔。最后一行去除底边以与卡片底圆角融合。
- **Hover**：`hover` 背景。
- **Density**：本系统所有表格都是"读多写少"的状态展示，行高 32-36px（含 padding）。**绝不引入 zebra striping** ——边框 + hover 已足够区分。

### Status Badges

- **Shape**：pill (`rounded.pill` = 999px)。4px × 12px padding。12px label 字号。
- **Default**：`bg` 背景 + `border` 描边 + `fg` 文字。表示"未知 / 查询中"。
- **Online**（`.status-online`）：`online` 文字 + 600 weight。
- **Offline**（`.status-offline`）：`offline` 文字 + 默认 weight。
- **职责**：仅用于设备状态、daemon 状态、autostart 状态——不要扩散成"任意标签"。chip/tag 是另一个语义，本系统尚未引入。

### Tabs

- **Style**：上方一行按钮，每个 `.tab` 是透明背景 + `border-bottom: 2px solid transparent`，激活时下边框换成 accent 蓝。
- **Default**：`muted` 文字 + 默认字重。
- **Active**：`fg` 文字 + 600 weight + accent 下划线。
- **Tauri-only tab**（`.tab-tauri`）：「本机」tab 默认 hidden，仅 Tauri 环境下 JS 取消隐藏。视觉与其它 tab 完全一致——这条 tab 不该被装饰得"特殊"，只是它的存在与否随环境决定。

### Definition List

- **`<dl>`**：grid 200px + 1fr，gap 12 × 24px。卡片样式（card-bg + border + 6px radius + 24px padding）。专用于「服务端」tab 的运行指标展开。
- **`<dt>`**：muted 文字，作为字段名。
- **`<dd>`**：mono 字体 + 12px，承载 ID / URL / 时间戳 / json 值。`word-break: break-all` 防止超长 ID 撑破布局。

### Metric Cards

- **Shape**：6px radius + 16px padding。
- **Layout**：父容器 `.metrics-grid` 用 `grid-template-columns: repeat(auto-fit, minmax(180px, 1fr))` 自适应放置。每张卡 ≥ 180px 宽。
- **Label**（`.metric-label`）：11px / muted / uppercase / 0.5px tracked。
- **Value**（`.metric-value`）：24px / 600 / mono / margin-top 4px。**这是整个系统唯一允许的 ≥ 18px 字号**，因为 metric 数字本身就是阅读对象，需要被快速扫到。

### Dialogs

- **Shape**：原生 `<dialog>` 元素，8px radius。`width: 90%` + `max-width: 480px`。
- **Backdrop**：`rgba(0,0,0,0.5)` 全屏压暗（焦点收束，不是装饰阴影）。
- **Variants**：service 发布 dialog、forward 添加 dialog、token 展示 dialog、通用 alert/confirm dialog。
- **Alert variant**（`.alert-modal-body`）：标题 `.alert-title` 16px/600；error 类型时标题 `.alert-error` 红色。消息体 `.alert-message` 支持 `white-space: pre-wrap` 显示多行 stderr。

### Named Rules

**The One Primary Per Card Rule.** 任意卡片或 dialog 内至多一个 primary button。其余动作走 default / link / danger。

**The Native Dialog Rule.** 全部弹窗用浏览器原生 `<dialog>` 元素 + `dialog.showModal()`。不写自家 portal 层、不引入 react-portal 等抽象、不模拟 backdrop。原生元素自带 Esc 关闭与正确的 z-index/焦点陷阱。

**The Pill Is Status Only Rule.** 999px radius 在本系统专属"状态指示"。chip/tag/小标签不允许复用 pill 形状——未来需要 chip 时另起一个 4px 矩形 + tint background 变体。

## 6. Do's and Don'ts

### Do:

- **Do** 用 OKLCH 范畴的认知组织色彩：bg/card-bg/hover 是同一色相不同明度，跟随 prefers-color-scheme 自然反转。
- **Do** 把状态用**颜色 + 文本 + 字重三重编码**。在线行：green + "在线" + 600。色弱用户与截图用户都能区分。
- **Do** 把所有错误根因暴露出来：HTTP code、Rust `Error`、omlctl stderr 尾部、文件路径——通过 `showAlert(msg, { kind: 'error' })` 全文展示。
- **Do** 用原生 `<dialog>` 元素做所有 modal。继承浏览器的 Esc 关闭、焦点陷阱、`::backdrop` 暗化。
- **Do** 让按钮的禁用态在视觉上"看起来真的不能点"：opacity 0.4 + not-allowed cursor + 屏蔽 hover。
- **Do** 用 mono 字体承载所有"机器生成的精确字符串"（pid、port、device_id、tunnel_secret 短指纹、stderr）。
- **Do** 用 border + 背景色阶表达层级。card-bg 比 bg 浅一档，table header 比 row 暗一档。
- **Do** 让浏览器与 Tauri 两端**视觉完全一致**，只在功能层（多/少一个 tab）差异。
- **Do** 把按钮的字号锁在 13px、表头锁在 12px tracked uppercase、body 锁在 14px。

### Don't:

- **Don't** 加紫色渐变 hero、四宫格大数字 metric、"快开始使用 →" 的 SaaS-cream landing 风格。这里是 admin UI，不是 marketing page。
- **Don't** 装饰性使用 box-shadow。除了原生 `<dialog>` 的 backdrop 焦点收束，**所有面都是平的**。
- **Don't** 用 `border-left` 或 `border-right` 大于 1px 当彩色侧条。这是 shared-laws absolute ban。需要强调用 background tint 或 leading icon。
- **Don't** 用 `background-clip: text` 制造渐变文字。所有文字 solid color。
- **Don't** 用 emoji 当状态图标（"✅在线 ❌离线"）。文字 + 颜色已足够；emoji 在等宽 mono 里渲染不稳定，在 CJK 系统字体里又显得幼态。
- **Don't** 把 status-badge 的 pill 形状复用给 chip / tag / 普通标签。pill 在本系统专属"状态指示"。
- **Don't** 用 bouncing / elastic / cubic-bezier 弹性曲线。所有 transition ≤ 150ms，纯 ease-out。
- **Don't** 引入 web font。系统字体的跨平台不一致是接受的代价；加载 web font 换来的"一致性"远不如它带来的首帧闪烁与额外 200KB 严重。
- **Don't** 把字号放大到 ≥ 18px——除了 metric value 这一个例外。整个系统的视觉重量压在密度而非字号上。
- **Don't** 把多个 primary button 塞进同一张卡片或同一个 dialog。一张卡 = 一个明确主操作。
- **Don't** 给浏览器和 Tauri 做两套视觉。它们必须看起来是同一个产品，只是入口不同。
- **Don't** 拿"过度简化"作为"克制"的借口：每个操作都开 modal、屏幕大半留白、单色调到没有状态色——那不是 calm，那是无信息。这套系统的密度是被需要的。
