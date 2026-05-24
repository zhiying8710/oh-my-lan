// info.js — 「服务端」tab 数据：metrics + 详情 dl + audit 表格。
//
// loadInfo 顺手 trigger loadBarkSettings（admin 失败不阻断 metrics）。
// 审计行表降级到此 tab 的折叠区——避免维护一个仅写操作历史就单开一个顶层 tab。

import { els, escapeHTML, fmtTime, fmtUptime, inTauri, renderEmpty } from './core.js';
import { api } from './api.js';
import { getServerUrl } from './state.js';
import { loadBarkSettings } from './bark.js';
import { loadSSHKeys } from './ssh.js';

export async function loadInfo() {
  const [info, metrics] = await Promise.all([
    api('/api/admin/info'),
    api('/api/admin/metrics').catch(() => null),
  ]);
  if (metrics) {
    const pct = metrics.port_pool_size > 0
      ? Math.round(100 * metrics.port_pool_used / metrics.port_pool_size) : 0;
    const cells = [
      ['设备 / 在线',           `${metrics.devices_online} / ${metrics.devices_total}`],
      ['服务 (启用 / 全部)',    `${metrics.services_enabled} / ${metrics.services_total}`],
      ['Forward (启用 / 全部)', `${metrics.forwards_enabled} / ${metrics.forwards_total}`],
      ['Admin token',           String(metrics.admin_tokens_total)],
      ['端口池占用',            `${metrics.port_pool_used} / ${metrics.port_pool_size} (${pct}%)`],
      ['运行时长',              fmtUptime(metrics.uptime_seconds)],
    ];
    els.metricsGrid.innerHTML = cells.map(([k, v]) => `
      <div class="metric-card">
        <div class="metric-label">${escapeHTML(k)}</div>
        <div class="metric-value">${escapeHTML(v)}</div>
      </div>`).join('');
  } else {
    els.metricsGrid.innerHTML = '';
  }
  els.infoDl.innerHTML = `
    <dt>Server fingerprint</dt><dd>${escapeHTML(info.server_fingerprint)}</dd>
    <dt>chisel 通告地址</dt><dd>${escapeHTML(info.chisel_addr)}</dd>
    <dt>端口池</dt><dd>${info.port_pool_min}-${info.port_pool_max}</dd>
    <dt>版本</dt><dd>${escapeHTML(info.version)}</dd>
    ${inTauri ? `<dt>当前服务器 URL</dt><dd>${escapeHTML(getServerUrl())}</dd>` : ''}
  `;
  els.serverInfo.textContent = `chisel ${info.chisel_addr} · ${info.version}`;
  // 顺手刷新 bark 配置；catch 后丢掉错误让它独立于上面 metrics 失败
  loadBarkSettings().catch(() => { /* admin endpoint 失败不阻断 metrics */ });
  // SSH 跳板信息——只在浏览器同源模式 unhide，桌面客户端 ssh.js 内部跳过
  loadSSHKeys().catch(() => { /* SSH 段失败不阻断 metrics */ });
}

export async function loadAudit() {
  const data = await api('/api/admin/audit?limit=200');
  const tbody = els.bodies.audit;
  if (!data.entries || data.entries.length === 0) {
    renderEmpty(tbody, 5, '尚无审计记录', '审计仅记录写操作。把第一个 token、第一个服务发出来之后这里就会有内容。');
    return;
  }
  tbody.innerHTML = data.entries.map(e => `
    <tr>
      <td class="mono">${fmtTime(e.ts)}</td>
      <td><code>${escapeHTML(e.action)}</code></td>
      <td class="mono">${escapeHTML(e.actor)}</td>
      <td class="mono">${escapeHTML(e.target || '-')}</td>
      <td class="mono">${e.detail ? escapeHTML(e.detail) : ''}</td>
    </tr>
  `).join('');
}
