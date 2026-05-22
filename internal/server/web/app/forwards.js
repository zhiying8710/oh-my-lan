// forwards.js — 「Forward」tab：列表 + 「+ 添加 forward」modal。
//
// 列表额外渲染 A1' 链路健康（linkHealthBadge）——需要 forward 关联 service 的 last_probe_*；
// forward DTO 不带 probe 信息，前端 join。

import { els, escapeHTML, enabledBadge, renderEmpty, linkHealthBadge } from './core.js';
import { api, reportApiError } from './api.js';
import { ensureLocalDevice } from './state.js';

let _refresh = () => {};
export function setRefreshHook(fn) { _refresh = fn; }

export async function loadForwards() {
  // 并发拉 forwards + services；后者用来取 last_probe_ok / last_probe_at（A1' 链路健康）。
  const [fdata, sdata] = await Promise.all([
    api('/api/admin/forwards'),
    api('/api/admin/services').catch(() => ({ services: [] })),
  ]);
  const tbody = els.bodies.forwards;
  if (!fdata.forwards || fdata.forwards.length === 0) {
    renderEmpty(tbody, 6, '尚无 forward 规则',
      '点击上方「+ 添加 forward」把别的设备的服务映射到本机端口；先确保「服务」tab 里有目标服务。');
    return;
  }
  const svcByID = new Map((sdata.services || []).map(s => [s.id, s]));
  tbody.innerHTML = fdata.forwards.map(f => {
    const route = `<span class="mono">${f.local_port}</span> → ` +
      `<span class="mono">${f.remote_public_port}</span> ` +
      `(${escapeHTML(f.remote_service_name)}@${escapeHTML(f.remote_device_name)})`;
    const remoteLabel = `${f.remote_service_name}@${f.remote_device_name}`;
    const svc = svcByID.get(f.remote_service_id);
    return `
    <tr>
      <td>${escapeHTML(f.owner_device_name)}</td>
      <td>${route}</td>
      <td>${escapeHTML(f.protocol)}</td>
      <td>${enabledBadge(f.enabled)}</td>
      <td>${linkHealthBadge(svc, f.protocol)}</td>
      <td>
        ${f.enabled
          ? `<button class="row-btn" data-action="disable-forward" data-id="${escapeHTML(f.id)}">停用</button>`
          : `<button class="row-btn" data-action="enable-forward" data-id="${escapeHTML(f.id)}">启用</button>`}
        <button class="row-btn btn-danger" data-action="delete-forward" data-id="${escapeHTML(f.id)}" data-local-port="${f.local_port}" data-remote-name="${escapeHTML(remoteLabel)}">删除</button>
      </td>
    </tr>
  `;
  }).join('');
}

export async function openForwardModal() {
  // 三路并发：设备列表、服务列表、本机身份
  const [devs, svcs, local] = await Promise.all([
    api('/api/admin/devices'),
    api('/api/admin/services'),
    ensureLocalDevice(),
  ]);
  const devSel = els.forwardForm.elements['owner_device_id'];
  devSel.innerHTML = '';

  // 桌面客户端：owner 锁死本机（forward 是"本机映射别人服务到本机端口"的语义）
  let ownerCandidates = devs.devices || [];
  if (local && local.id) {
    ownerCandidates = ownerCandidates.filter(d => d.id === local.id);
  }
  ownerCandidates.forEach(d => {
    const opt = document.createElement('option');
    opt.value = d.id;
    opt.textContent = `${d.name}`;
    devSel.appendChild(opt);
  });

  const svcSel = els.forwardForm.elements['remote_service_id'];
  svcSel.innerHTML = '';
  // 桌面客户端：目标服务必须排除本机服务（"forward 到自己" 语义无意义且会撞端口）
  let svcCandidates = (svcs.services || []).filter(s => s.enabled);
  if (local && local.id) {
    svcCandidates = svcCandidates.filter(s => s.device_id !== local.id);
  }
  svcCandidates.forEach(s => {
    const opt = document.createElement('option');
    opt.value = s.id;
    opt.textContent = `${s.device_name}/${s.name} (${s.protocol} :${s.public_port})`;
    svcSel.appendChild(opt);
  });

  // 候选服务为空时给出明确提示，不然用户面对空下拉框会困惑
  if (svcCandidates.length === 0) {
    const opt = document.createElement('option');
    opt.value = '';
    opt.disabled = true;
    opt.selected = true;
    opt.textContent = local && local.id
      ? '（没有可 forward 的远端服务——其它设备尚未发布服务）'
      : '（没有已启用的服务）';
    svcSel.appendChild(opt);
  }

  els.forwardForm.reset();
  els.forwardModal.showModal();
}

export async function submitForwardForm(e) {
  e.preventDefault();
  const data = Object.fromEntries(new FormData(els.forwardForm).entries());
  data.local_port = Number(data.local_port);
  try {
    await api('/api/admin/forwards', { method: 'POST', body: data });
    els.forwardModal.close();
    await _refresh();
  } catch (err) { reportApiError(err, '添加 forward 失败'); }
}
