// app.js — oh-my-lan admin UI 入口模块。
//
// 历史：1232 行 god-file，承载所有职责（DOM 绑定、API 调用、tab 切换、modals、
// Tauri 本机 daemon 控制、登录…）。已切到 web/app/*.js ES modules 下，本文件只剩
// "wire-up + 启动路径决策"。
//
// 一份代码同时跑在：
//   1) omlserver embed.FS 提供的 /admin/（浏览器同源）
//   2) Tauri webview 加载的 tauri://localhost（跨源 → 必须用绝对 URL 调 server）
// Tauri 环境额外提供：服务器地址配置视图 + 本机 daemon 启停 tab。

import { els, inTauri, ACTIVE_TAB_KEY } from './app/core.js';
import { showAlert } from './app/alert.js';
import { getToken, getServerUrl } from './app/state.js';
import { showServerConfig, showLogin, showMain } from './app/views.js';
import { bindAuthEvents } from './app/auth.js';
import { activateTab, refreshActive } from './app/tabs.js';
import { issueToken } from './app/devices.js';
import { openServiceModal, submitServiceForm, setRefreshHook as setServicesRefresh } from './app/services.js';
import { openForwardModal, submitForwardForm, setRefreshHook as setForwardsRefresh } from './app/forwards.js';
import { saveBarkSettings, testBarkPush } from './app/bark.js';
import { loadLogs } from './app/logs.js';
import { handleRowAction, setRefreshHook as setActionsRefresh } from './app/actions.js';
import {
  startDaemon, stopDaemon, refreshLocalTab,
  generateEnrollToken, enableAutostart, disableAutostart,
  submitEnroll, showEnrollCard,
} from './app/local.js';
import { reportApiError } from './app/api.js';

// 让"submit form / row action 完成后刷新当前 tab"统一走 tabs.refreshActive，
// 避免功能模块直接 import tabs 引入跨层依赖。
setServicesRefresh(refreshActive);
setForwardsRefresh(refreshActive);
setActionsRefresh(refreshActive);

bindAuthEvents();

els.tabs.forEach(t => t.addEventListener('click', () => activateTab(t.dataset.tab)));
els.refreshBtn.addEventListener('click', refreshActive);

// 「+ 生成 enrollment token」唯一入口在「设备」tab，与「+ 发布服务」「+ 添加 forward」对齐
if (els.devicesIssueTokenBtn) els.devicesIssueTokenBtn.addEventListener('click', issueToken);

els.serviceAddBtn.addEventListener('click', () =>
  openServiceModal().catch(e => reportApiError(e, '打开服务表单失败')));
els.forwardAddBtn.addEventListener('click', () =>
  openForwardModal().catch(e => reportApiError(e, '打开 forward 表单失败')));

if (els.barkForm) {
  els.barkForm.addEventListener('submit', saveBarkSettings);
  els.barkTestBtn.addEventListener('click', testBarkPush);
}

if (els.logsRefreshBtn) {
  els.logsRefreshBtn.addEventListener('click', loadLogs);
  // 首次展开 details 时也拉一次（避免每次切「服务端」tab 自动拉，省 IPC）
  const logsDetails = els.logsPre && els.logsPre.closest('details');
  if (logsDetails) {
    logsDetails.addEventListener('toggle', () => {
      if (logsDetails.open && !els.logsPre.textContent.trim()) loadLogs();
    });
  }
}

els.serviceForm.addEventListener('submit', submitServiceForm);
els.forwardForm.addEventListener('submit', submitForwardForm);

// Tauri 本机 daemon 控制按钮（如果元素不存在跳过）
if (inTauri && els.daemonStartBtn) {
  els.daemonStartBtn.addEventListener('click', startDaemon);
  els.daemonStopBtn.addEventListener('click', stopDaemon);
  els.daemonRefreshBtn.addEventListener('click', refreshLocalTab);
  els.daemonReenrollBtn.addEventListener('click', () => {
    // 用户主动想重置注册——不删除现有 state，只是切回注册卡片让他重新填
    showEnrollCard();
    els.enrollMsg.textContent = '注意：注册新身份会覆盖本机现有 state.json';
  });
  els.enrollForm.addEventListener('submit', submitEnroll);
  els.enrollGenerateTokenBtn.addEventListener('click', generateEnrollToken);
  els.autostartEnableBtn.addEventListener('click', enableAutostart);
  els.autostartDisableBtn.addEventListener('click', disableAutostart);
}

document.addEventListener('click', (e) => {
  const btn = e.target.closest('button[data-action]');
  if (btn) {
    handleRowAction(btn.dataset.action, btn.dataset);
    return;
  }
  const closeBtn = e.target.closest('button[data-close]');
  if (closeBtn) {
    const dlg = closeBtn.closest('dialog');
    if (dlg) dlg.close();
  }
});

els.copyTokenBtn.addEventListener('click', async () => {
  try {
    await navigator.clipboard.writeText(els.issuedTokenDisplay.textContent);
    els.copyTokenBtn.textContent = '已复制';
    setTimeout(() => { els.copyTokenBtn.textContent = '复制'; }, 1500);
  } catch (_) { showAlert('复制失败，请手动选中文本', { title: '剪贴板', kind: 'error' }); }
});

// --- 启动路径决策 ---

if (inTauri && !getServerUrl()) {
  showServerConfig();
} else if (getToken()) {
  showMain();
  activateTab(localStorage.getItem(ACTIVE_TAB_KEY) || 'devices');
} else {
  showLogin();
}
