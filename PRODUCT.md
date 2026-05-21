# Product

## Register

product

## Users

oh-my-lan 是单人使用的个人内网穿透控制平面。当前唯一用户就是项目作者本人：一名熟悉命令行、习惯阅读 systemd / launchd / chisel 这类底层概念的开发者。

使用场景双入口同等重要：

- **浏览器**：访问 `http://<vps>:58080/admin/` 直连服务端 embed 出来的 SPA。出差/远程办公/手边只有别人电脑时的入口，需求是看一眼设备/服务状态、临时发布个 forward。
- **Tauri 桌面客户端（macOS / Windows / Linux）**：日常长开的窗口，多了「本机」tab——把本机注册为 oh-my-lan 设备 + 启停 daemon + 配置开机自启。需求是 "把控制台和本机 daemon 管理放在同一个窗口里"。

主要任务：
- 看 mesh 里有哪些设备、哪些在线、最后活跃时间
- 发布 / 撤销服务（本机的 ssh、内网 web）
- 添加 / 删除 forward（把别的设备的服务映射到本机端口或公网端口）
- 审计最近的写操作（自查 / 排查"我什么时候改的"）
- 服务端运行指标（uptime、隧道连接数、内存）
- Tauri 独占：本机 daemon 生命周期、enroll、开机自启

## Product Purpose

把多台个人设备（家里的 NAS、工作的 MacBook、出差的笔记本）通过一台 VPS 中继串成 mesh，让任意一台都能通过本地端口访问另一台的服务，不依赖公网 IP / 端口转发 / 第三方账号。

成功标准是 **"开了就忘"**：daemon 自启，UI 大多数时候只是用来确认"还活着"，偶尔加一条 forward。不是每天打开点来点去的工具。

## Brand Personality

**Calm · Snappy · Precise**

- **Calm**：状态变化用图标 + 文本 + 颜色三重编码，但颜色是低饱和的状态色，不抢注意力。错误用专用 alert 弹窗而不是飘红 banner。
- **Snappy**：点击、切 tab、刷新是即时手感，过渡动画 ≤ 150ms 且只动 transform/opacity，不动布局属性。
- **Precise**：技术实体（pid、port、tunnel_secret 短指纹、device_id）保持原文不被翻译/修饰；数值不被装饰成大字 metric；表头用术语而不是"美化过的"说法。

声音偏 deadpan tooling，不卖萌不打鸡血。错误信息含具体路径 / errno / stderr 尾部，不写 "Oops! 出错了 :("。

## Anti-references

明确**不要**的方向（用户在 teach 时勾全了）：

- **SaaS 模板感**：紫色渐变 hero、千篇一律的卡片网格、四个大数字 metric 占满首屏。Vercel/Stripe 仿品 dashboard。
- **企业级 IT 厚重感**：双层菜单 + 工具栏 + 厚灰边框 + 16px 内边距挤压的老派后台。pfSense Web UI 那种 2010 年代质感。
- **过度装饰**：玻璃拟态、彩色阴影、bouncing 动画、emoji 状态徽章、彩虹渐变文字。
- **过度简化**：全白背景 + 一种颜色 + 大量留白 + 每个操作都开 modal。本工具信息密度需求高，留白要让位给数据。

具体走 **开发者工具** 这条 lane：Linear、Raycast、Tailscale admin、Cloudflare dashboard（旧版而非新版 SaaS-cream 改版）的密度+精度+冷静。

## Design Principles

1. **数据密度第一**：一屏装下设备表 + 状态摘要 + 当前操作的全部上下文。该用表格的地方不要用卡片，该用一行的地方不要用两行。
2. **状态是事实，不是装饰**：在线/离线/运行中/已停止是布尔事实，用颜色 + 文本 + 图标三重编码而不是仅靠颜色，确保 a11y 与色弱用户也能区分。
3. **错误要可调试**：所有失败都暴露根因（HTTP code / Rust error / stderr tail），不二次包装成 "操作失败"。
4. **危险操作要确认**：删除设备、关闭自启、撤销 token 走自定义 confirm 组件，文案说明影响面（"会停掉跑在自启上的 daemon"），不让用户走神就误删。
5. **同一份代码两副皮肤**：浏览器 vs Tauri 的差异只在功能层（多/少一个 tab、URL 来源），视觉系统完全一致；不为某一边专门做装饰。

## Accessibility & Inclusion

- 系统字体策略：macOS PingFang / Windows MSYH / Linux Noto Sans CJK，不加载 web font 减少首帧闪烁。接受中文渲染随平台略有差异。
- 不强制 WCAG 等级；个人工具不会被审计员盯。但状态指示**必须**同时用颜色+文本+图标（已在执行中）。
- 暂不专门做 `prefers-reduced-motion` 适配——总体动画已经极少，无明显运动。
- 键盘操作：所有可点 button 都是真 `<button>`，Tab 序自然，Esc 关 modal 已就位。
