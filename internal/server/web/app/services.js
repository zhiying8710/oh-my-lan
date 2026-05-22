// services.js — 「服务」tab：列表 + 「+ 发布服务」modal。
//
// openServiceModal 关键约束：桌面（Tauri）模式下 owner 锁成本机；浏览器（admin）模式
// 保留全部设备候选。具体决策见 state.ensureLocalDevice 注释。

import { els, escapeHTML, fmtTime, enabledBadge, renderEmpty } from './core.js';
import { api, reportApiError } from './api.js';
import { ensureLocalDevice } from './state.js';

// refreshActive 由 tabs.js 提供，但 actions/forms 提交后需要刷新当前 tab——
// 通过 setter 注入避免与 tabs.js 形成循环依赖。
let _refresh = () => {};
export function setRefreshHook(fn) { _refresh = fn; }

export async function loadServices() {
  const data = await api('/api/admin/services');
  const tbody = els.bodies.services;
  if (!data.services || data.services.length === 0) {
    renderEmpty(tbody, 8, '尚无发布的服务',
      '点击上方「+ 发布服务」把任意设备的本地端口暴露出来；或在设备上执行 <span class="mono">omlctl service add</span>。');
    return;
  }
  tbody.innerHTML = data.services.map(s => `
    <tr>
      <td>${escapeHTML(s.device_name)}</td>
      <td>${escapeHTML(s.name)}</td>
      <td>${escapeHTML(s.protocol)}</td>
      <td class="mono">${escapeHTML(s.local_addr)}</td>
      <td class="mono">${s.public_port}</td>
      <td>${enabledBadge(s.enabled)}</td>
      <td>${fmtTime(s.created_at)}</td>
      <td>
        ${s.enabled
          ? `<button class="row-btn" data-action="disable-service" data-id="${escapeHTML(s.id)}">停用</button>`
          : `<button class="row-btn" data-action="enable-service" data-id="${escapeHTML(s.id)}">启用</button>`}
        <button class="row-btn btn-danger" data-action="delete-service" data-id="${escapeHTML(s.id)}" data-name="${escapeHTML(s.name)}">删除</button>
      </td>
    </tr>
  `).join('');
}

export async function openServiceModal() {
  // 与 admin API 拿全设备列表并行去拉本机身份（Tauri 才有；浏览器返回 null）
  const [data, local] = await Promise.all([
    api('/api/admin/devices'),
    ensureLocalDevice(),
  ]);
  const sel = els.serviceForm.elements['device_id'];
  sel.innerHTML = '';

  // 桌面客户端只能为本机发布服务；浏览器视角（admin）保留全部
  let candidates = data.devices || [];
  if (local && local.id) {
    candidates = candidates.filter(d => d.id === local.id);
  }
  candidates.forEach(d => {
    const opt = document.createElement('option');
    opt.value = d.id;
    opt.textContent = `${d.name} (${d.id.slice(0, 8)}…)`;
    sel.appendChild(opt);
  });
  // 单选项已经天然防误选，不用 disabled——disabled select 会被 FormData 跳过
  // 导致 submit 时 device_id 缺失
  els.serviceForm.reset();
  els.serviceModal.showModal();
}

export async function submitServiceForm(e) {
  e.preventDefault();
  const data = Object.fromEntries(new FormData(els.serviceForm).entries());
  try {
    await api('/api/admin/services', { method: 'POST', body: data });
    els.serviceModal.close();
    await _refresh();
  } catch (err) { reportApiError(err, '发布服务失败'); }
}
