// auth.js — 登录 / 登出 / 改服务器 URL 的事件绑定。
//
// 登录用裸 fetch 而不是 api()——api() 会带 Authorization 头，但登录前 token 不存在；
// 而且登录失败时要展示在表单内联错误而不是弹 alert，错误处理路径与 api() 不同。

import { els, inTauri } from './core.js';
import { setToken, getServerUrl, setServerUrl } from './state.js';
import { api } from './api.js';
import { showServerConfig, showLogin, showMain } from './views.js';
import { activateTab } from './tabs.js';
import { ACTIVE_TAB_KEY } from './core.js';

export function bindAuthEvents() {
  els.serverConfigForm.addEventListener('submit', (e) => {
    e.preventDefault();
    const u = els.serverUrlInput.value.trim();
    if (!u) return;
    try {
      new URL(u);
    } catch (_) {
      els.serverConfigErr.textContent = 'URL 格式不合法';
      els.serverConfigErr.hidden = false;
      return;
    }
    setServerUrl(u);
    showLogin();
  });

  if (els.changeServerLink) {
    els.changeServerLink.addEventListener('click', (e) => {
      e.preventDefault();
      setToken('');
      showServerConfig();
    });
  }

  els.loginForm.addEventListener('submit', async (e) => {
    e.preventDefault();
    const u = els.usernameInput.value.trim();
    const p = els.passwordInput.value;
    if (!u || !p) return;

    setToken(''); // 清掉旧 session
    try {
      const url = getServerUrl() + '/api/auth/login';
      const resp = await fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username: u, password: p }),
      });
      if (resp.status === 401) {
        els.loginErr.textContent = '用户名或密码错误';
        els.loginErr.hidden = false;
        return;
      }
      if (!resp.ok) {
        let msg = resp.statusText;
        try { const j = await resp.json(); if (j.error) msg = j.error; } catch (_) {}
        throw new Error(`HTTP ${resp.status}: ${msg}`);
      }
      const body = await resp.json();
      setToken(body.session_token);
      els.passwordInput.value = '';
      showMain();
      // showMain 故意不 activateTab，避免 views ↔ tabs 循环依赖；这里串起来
      activateTab(localStorage.getItem(ACTIVE_TAB_KEY) || 'devices');
    } catch (err) {
      let msg = err.message;
      if (inTauri && /Failed to fetch|NetworkError|TypeError/i.test(msg)) {
        msg = '连接失败：检查服务器 URL 是否正确（' + getServerUrl() + '）';
      }
      els.loginErr.textContent = msg;
      els.loginErr.hidden = false;
    }
  });

  els.logoutBtn.addEventListener('click', async () => {
    // 主动通知后端删 session（best-effort；删不掉也无所谓，反正客户端 token 已弃）
    try { await api('/api/auth/logout', { method: 'POST' }); } catch (_) {}
    setToken('');
    showLogin();
  });
}
