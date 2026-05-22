// alert.js — Promise 化的 <dialog> alert/confirm。
//
// 浏览器原生 window.alert 在 Tauri WKWebView 下样式僵硬、不可拖动、不支持中文 fallback 字体；
// 这里用一个 <dialog> 元素包装出 Promise 化的 showAlert / showConfirm。
// 设计为单例：同时只能有一个 alert 弹窗（多了会互相覆盖），调用方靠 await 串行。

import { els } from './core.js';

let _resolver = null;

export function showAlert(message, opts = {}) {
  const { title = '提示', kind = 'info', confirm = false } = opts;
  els.alertTitle.textContent = title;
  els.alertTitle.className = 'alert-title' + (kind === 'error' ? ' alert-error' : '');
  els.alertMessage.textContent = String(message);
  els.alertCancelBtn.hidden = !confirm;
  // 旧 resolver 还没结算？直接 resolve(false) 释放，避免悬挂 Promise
  if (_resolver) { _resolver(false); _resolver = null; }
  return new Promise((resolve) => {
    _resolver = resolve;
    els.alertModal.showModal();
  });
}

export function showConfirm(message, opts = {}) {
  return showAlert(message, { ...opts, confirm: true });
}

function resolveAlert(value) {
  els.alertModal.close();
  if (_resolver) {
    const r = _resolver;
    _resolver = null;
    r(value);
  }
}

els.alertOkBtn.addEventListener('click', () => resolveAlert(true));
els.alertCancelBtn.addEventListener('click', () => resolveAlert(false));
els.alertModal.addEventListener('cancel', () => resolveAlert(false)); // Esc 键
