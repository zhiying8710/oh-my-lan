// bark.js — bark 推送配置面板：load/save 设置 + 测试推送。
//
// 嵌在「服务端」tab，与 info.js 协作（loadInfo 内会触发 loadBarkSettings 顺手刷新）。

import { els } from './core.js';
import { api, reportApiError } from './api.js';

export async function loadBarkSettings() {
  if (!els.barkForm) return;
  try {
    const bs = await api('/api/admin/bark');
    els.barkEnabled.checked = !!bs.enabled;
    els.barkUrl.value = bs.bark_url || '';
    els.barkThreshold.value = bs.offline_threshold_seconds || 180;
    els.barkMsg.textContent = '';
  } catch (e) {
    els.barkMsg.textContent = '读 bark 配置失败：' + (e && e.message || e);
  }
}

export async function saveBarkSettings(ev) {
  ev.preventDefault();
  const body = {
    enabled: !!els.barkEnabled.checked,
    bark_url: els.barkUrl.value.trim(),
    offline_threshold_seconds: Number(els.barkThreshold.value) || 180,
  };
  try {
    await api('/api/admin/bark', { method: 'PUT', body });
    els.barkMsg.textContent = '已保存';
  } catch (e) {
    reportApiError(e, '保存 bark 配置失败');
  }
}

export async function testBarkPush() {
  els.barkTestBtn.disabled = true;
  els.barkMsg.textContent = '正在测试推送…';
  try {
    await api('/api/admin/bark/test', { method: 'POST' });
    els.barkMsg.textContent = '✓ 推送已发出，请到 bark App 查收';
  } catch (e) {
    reportApiError(e, '测试推送失败');
    els.barkMsg.textContent = '';
  } finally {
    els.barkTestBtn.disabled = false;
  }
}
