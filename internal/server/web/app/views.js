// views.js — 顶层视图切换：server-config / login / main 三态互斥。
//
// 故意不在 showMain 里调 activateTab——避免和 tabs.js 产生循环依赖（views ↔ tabs ↔ loaders ↔ api ↔ views）。
// 调用方负责：showMain(); activateTab(localStorage.getItem(ACTIVE_TAB_KEY) || 'devices');

import { els, inTauri, CTL_PATH_KEY, CTL_CONFIG_KEY } from './core.js';
import { getServerUrl } from './state.js';

export function hideAllViews() {
  els.serverConfigView.hidden = true;
  els.loginView.hidden = true;
  els.mainView.hidden = true;
  els.refreshBtn.hidden = true;
  els.logoutBtn.hidden = true;
  els.serverInfo.textContent = '';
}

export function showServerConfig(errMsg) {
  hideAllViews();
  els.serverConfigView.hidden = false;
  els.serverConfigErr.hidden = !errMsg;
  if (errMsg) els.serverConfigErr.textContent = errMsg;
  els.serverUrlInput.focus();
  els.serverUrlInput.value = getServerUrl() || '';
}

export function showLogin(errMsg) {
  hideAllViews();
  els.loginView.hidden = false;
  els.loginErr.hidden = !errMsg;
  if (errMsg) els.loginErr.textContent = errMsg;
  if (inTauri) {
    els.currentServerDisplay.textContent = `当前服务器：${getServerUrl()}`;
    els.changeServerLink.hidden = false;
  }
  els.usernameInput.focus();
}

// showMain 只负责"切显示"。Tab 激活由调用方紧跟着做（见 app.js start() 与 auth.js 登录成功路径）。
// 这样可以避免 views.js 反向依赖 tabs.js。
export function showMain() {
  hideAllViews();
  els.mainView.hidden = false;
  els.refreshBtn.hidden = false;
  els.logoutBtn.hidden = false;
  if (inTauri) {
    document.querySelector('.tab-tauri').hidden = false;
    // 恢复本机 daemon 控制的输入项
    els.ctlPathInput.value = localStorage.getItem(CTL_PATH_KEY) || '';
    els.ctlConfigInput.value = localStorage.getItem(CTL_CONFIG_KEY) || '';
  }
}
