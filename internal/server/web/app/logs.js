// logs.js — C1：拉服务端日志渲染到 <pre>。
//
// 触发路径：手动点「刷新」、或第一次展开折叠区。不在 refreshActive 里默认拉——避免
// 每次切「服务端」tab 都打一次 /api/admin/logs（实际有 200 条 entries，IO 不小）。

import { els, escapeHTML } from './core.js';
import { api } from './api.js';

export async function loadLogs() {
  if (!els.logsPre) return;
  els.logsStatus.textContent = '加载中…';
  try {
    const r = await api('/api/admin/logs?limit=200');
    if (!r.entries || r.entries.length === 0) {
      els.logsPre.textContent = '（暂无日志；如果你刚启动 server，过一会再刷新）';
      els.logsStatus.textContent = '';
      return;
    }
    // 反转：让最新的在最上面（更符合"最近 N 条"心智模型）
    const reversed = [...r.entries].reverse();
    els.logsPre.innerHTML = reversed.map(e => {
      const time = e.time ? new Date(e.time).toLocaleTimeString() : '';
      const lvl = (e.level || 'INFO').toUpperCase();
      const attrs = e.attrs ? '  ' + escapeHTML(e.attrs) : '';
      return `<span class="lvl-${escapeHTML(lvl)}">[${escapeHTML(time)}] ${escapeHTML(lvl)}</span>  ${escapeHTML(e.message)}${attrs}`;
    }).join('\n');
    els.logsStatus.textContent = `共 ${r.entries.length} 条 · 最新在上`;
  } catch (e) {
    els.logsStatus.textContent = '加载失败: ' + (e && e.message || e);
  }
}
