// actions.js — 行内动作（撤销设备、增删改启停 service / forward）。
//
// 危险动作的影响面文案：三段式（操作 / 影响面 / 不可逆），用 \n 分行让 .alert-message
// 的 white-space: pre-wrap 自然展示——这是 DESIGN.md 第 4 条 Design Principle 的具象。
// 历史教训：之前这里走浏览器原生 confirm()，影响面一行铺平 + Tauri WKWebView 样式僵硬，
// 是整个 UI 唯一可能造成数据丢失的设计漏洞。改用 showConfirm() 与 alert 共用同一 dialog。

import { api, reportApiError } from './api.js';
import { showConfirm } from './alert.js';

const DANGEROUS_ACTIONS = {
  'revoke-device': (data) => ({
    title: `撤销设备 ${data.name}`,
    message:
      `该设备及其名下的服务、forward 将立即删除。\n` +
      `设备 daemon 在下次心跳收到 401 后会自动退出。\n` +
      `该操作不可撤销。`,
  }),
  'delete-service': (data) => ({
    title: `删除服务 ${data.name}`,
    message:
      `服务条目以及关联的 forward 将一并删除。\n` +
      `占用的公网端口会归还到 port_pool。\n` +
      `该操作不可撤销。`,
  }),
  'delete-forward': (data) => ({
    title: `删除 forward`,
    message:
      `本机 ${data.localPort || '?'} → 远端 ${data.remoteName || '?'} 的映射将立即移除。\n` +
      `owner 设备的 daemon 会 reload 隧道使变更生效。\n` +
      `该操作不可撤销。`,
  }),
};

// refresh hook 由 app.js / tabs.js 在 wire-up 时注入，避免 actions ↔ tabs 循环依赖。
let _refresh = () => {};
export function setRefreshHook(fn) { _refresh = fn; }

export async function handleRowAction(action, data) {
  // 1) 危险动作走 showConfirm（自家 <dialog>），文案分三段
  const factory = DANGEROUS_ACTIONS[action];
  if (factory) {
    const { title, message } = factory(data);
    const ok = await showConfirm(message, { title, kind: 'error' });
    if (!ok) return;
  }

  const map = {
    'revoke-device':   { method: 'POST',   path: `/api/admin/devices/${data.id}/revoke` },
    'delete-service':  { method: 'DELETE', path: `/api/admin/services/${data.id}` },
    'enable-service':  { method: 'POST',   path: `/api/admin/services/${data.id}/enable` },
    'disable-service': { method: 'POST',   path: `/api/admin/services/${data.id}/disable` },
    'delete-forward':  { method: 'DELETE', path: `/api/admin/forwards/${data.id}` },
    'enable-forward':  { method: 'POST',   path: `/api/admin/forwards/${data.id}/enable` },
    'disable-forward': { method: 'POST',   path: `/api/admin/forwards/${data.id}/disable` },
  };
  const m = map[action];
  if (!m) return;
  try {
    await api(m.path, { method: m.method });
    await _refresh();
  } catch (e) {
    reportApiError(e, '操作失败');
  }
}
