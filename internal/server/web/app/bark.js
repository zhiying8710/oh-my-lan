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

// testBarkPush 在发请求前会先 PUT 当前表单值——历史教训：用户填了 URL 直接点
// "测试推送"，server 端 test handler 从 DB 读 URL 还是空 → 400 "bark URL 未配置"。
// 与其让用户记住"先保存再测试"，不如点击时自动 save-then-test。
export async function testBarkPush() {
  els.barkTestBtn.disabled = true;
  els.barkMsg.textContent = '正在保存配置…';
  const body = {
    enabled: !!els.barkEnabled.checked,
    bark_url: els.barkUrl.value.trim(),
    offline_threshold_seconds: Number(els.barkThreshold.value) || 180,
  };
  if (!body.bark_url) {
    els.barkMsg.textContent = '';
    els.barkTestBtn.disabled = false;
    reportApiError(new Error('bark URL 必填'), '测试推送失败');
    return;
  }
  try {
    // 测试推送不依赖 enabled——临时用 enabled=true 通过 PUT 的 URL 校验，
    // 保留用户原本 enabled 选择写回。两步原子语义足够"试一下"场景。
    await api('/api/admin/bark', { method: 'PUT', body: { ...body, enabled: true } });
    els.barkMsg.textContent = '正在测试推送…';
    await api('/api/admin/bark/test', { method: 'POST' });
    els.barkMsg.textContent = '✓ 推送已发出，请到 bark App 查收';
    // 把用户原本的 enabled 状态写回（如果他原本不想 enable）
    if (!body.enabled) {
      await api('/api/admin/bark', { method: 'PUT', body });
    }
  } catch (e) {
    reportApiError(e, '测试推送失败');
    els.barkMsg.textContent = '';
  } finally {
    els.barkTestBtn.disabled = false;
  }
}
