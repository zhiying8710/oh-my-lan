// devices.js — 「设备」tab：列表渲染 + 顶部的"生成 enrollment token"按钮。

import { els, escapeHTML, fmtTime, statusBadge, renderEmpty } from './core.js';
import { api, reportApiError } from './api.js';

export async function loadDevices() {
  const data = await api('/api/admin/devices');
  const tbody = els.bodies.devices;
  if (!data.devices || data.devices.length === 0) {
    renderEmpty(tbody, 8, '尚无注册设备',
      '点击右上「生成 enrollment token」拿到 token，在设备上执行 <span class="mono">omlctl enroll --token …</span> 即可注册。');
    return;
  }
  tbody.innerHTML = data.devices.map(d => `
    <tr>
      <td>${escapeHTML(d.name)}</td>
      <td>${statusBadge(d.status)}</td>
      <td>${d.services_count}</td>
      <td>${d.forwards_count}</td>
      <td>${fmtTime(d.last_seen_at)}</td>
      <td>${fmtTime(d.created_at)}</td>
      <td class="mono">${escapeHTML(d.id)}</td>
      <td>
        <button class="row-btn" data-action="kick-device" data-id="${escapeHTML(d.id)}" data-name="${escapeHTML(d.name)}" title="重置 chisel UserIndex，让 daemon 重连。注：已建立的 session 不会立刻断；真卡住的 session 会在 30s 内 keep-alive 超时自愈。">踢出</button>
        <button class="row-btn btn-danger" data-action="revoke-device" data-id="${escapeHTML(d.id)}" data-name="${escapeHTML(d.name)}">撤销</button>
      </td>
    </tr>
  `).join('');
}

export async function issueToken() {
  try {
    const r = await api('/api/admin/enroll/tokens', { method: 'POST' });
    els.issuedTokenDisplay.textContent = r.token;
    els.issuedTokenExpires.textContent = fmtTime(r.expires_at);
    els.tokenModal.showModal();
  } catch (e) { reportApiError(e, '生成 token 失败'); }
}
