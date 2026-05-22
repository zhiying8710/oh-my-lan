// tabs.js — 顶部 tab 切换 + refreshActive 路由到对应 loader。
//
// tabs.js 是个"hub"：聚合所有 loader 模块。各 loader 不直接互调，全部走 refreshActive
// 这条统一刷新路径，避免功能模块之间出现暗连接（cross-cutting subscribers）。

import { els, inTauri, ACTIVE_TAB_KEY } from './core.js';
import { reportApiError } from './api.js';
import { loadDevices } from './devices.js';
import { loadServices } from './services.js';
import { loadForwards } from './forwards.js';
import { loadInfo, loadAudit } from './info.js';
import { refreshLocalTab } from './local.js';

export function activateTab(name) {
  // 本机 tab 仅 Tauri 可选；浏览器尝试激活时回退到 devices
  if (name === 'local' && !inTauri) name = 'devices';
  // 历史残留：旧版本 'audit' 是顶层 tab，现已并入「服务端」tab。
  // 用户 localStorage 里的旧值会自动 fallback 到 info（含审计折叠区）
  if (name === 'audit') name = 'info';
  localStorage.setItem(ACTIVE_TAB_KEY, name);
  els.tabs.forEach(t => t.classList.toggle('active', t.dataset.tab === name));
  Object.entries(els.panels).forEach(([k, panel]) => { panel.hidden = k !== name; });
  refreshActive();
}

export async function refreshActive() {
  const name = localStorage.getItem(ACTIVE_TAB_KEY) || 'devices';
  // loading 指示：在 active panel 上挂 aria-busy；CSS 用它给 .panel-actions 加一个 muted dot spinner，
  // 避免 fetch 期间用户看到上一次的 stale 数据却没有任何提示。
  const panel = els.panels[name];
  if (panel) panel.setAttribute('aria-busy', 'true');
  try {
    switch (name) {
      case 'devices':  await loadDevices(); break;
      case 'services': await loadServices(); break;
      case 'forwards': await loadForwards(); break;
      case 'info':
        // 「服务端」tab 整合 metrics + dl 详情 + 审计日志（原顶层 audit tab 降级到此）
        await Promise.all([loadInfo(), loadAudit()]);
        break;
      case 'local':    await refreshLocalTab(); return;  // 本机 tab 不依赖远程
    }
    if (name !== 'info') loadInfo().catch(() => {});
  } catch (e) {
    reportApiError(e, '加载失败');
  } finally {
    if (panel) panel.removeAttribute('aria-busy');
  }
}
