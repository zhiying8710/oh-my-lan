// api.js — 通用 fetch wrapper + 401 跳转 + 用户友好错误展示。
//
// 所有走 server REST API 的模块都应该 import 这里的 `api`，统一鉴权头、Content-Type
// 和错误反馈语义。reportApiError 是"调用方 catch 时的兜底" — UNAUTHORIZED 跳登录、
// 其它统一弹 alert。

import { getToken, getServerUrl, setToken } from './state.js';
import { showAlert } from './alert.js';
import { showLogin } from './views.js';

export async function api(path, options = {}) {
  const url = getServerUrl() + path;
  const resp = await fetch(url, {
    method: options.method || 'GET',
    headers: {
      'Authorization': 'Bearer ' + getToken(),
      ...(options.body ? { 'Content-Type': 'application/json' } : {}),
    },
    body: options.body ? JSON.stringify(options.body) : undefined,
  });
  if (resp.status === 401) throw new Error('UNAUTHORIZED');
  if (!resp.ok) {
    let msg = resp.statusText;
    try { const j = await resp.json(); if (j.error) msg = j.error; } catch (_) {}
    throw new Error(`HTTP ${resp.status}: ${msg}`);
  }
  if (resp.status === 204) return null;
  return resp.json();
}

// 统一处理 API 错误：UNAUTHORIZED 走"会话已过期"跳登录页，其它走 showAlert。
// 历史教训：直接 showAlert(err.message) 会把字面量 "UNAUTHORIZED" 当标题显示给用户，
// 用户既看不懂也无路可走。这个 helper 必须被所有"调 api() 的 catch"调用。
export function reportApiError(err, fallbackTitle) {
  if (err && err.message === 'UNAUTHORIZED') {
    setToken('');
    showLogin('会话已过期，请重新登录');
    return;
  }
  showAlert(String(err && err.message || err), { title: fallbackTitle || '操作失败', kind: 'error' });
}
