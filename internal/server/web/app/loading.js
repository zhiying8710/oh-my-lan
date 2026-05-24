// loading.js — 异步按钮的"跑中态"统一封装。
//
// 用法：
//   await withLoading(els.enrollSubmitBtn, els.enrollMsg, '注册中…', async () => {
//     await tauriCmd('daemon_enroll', { ... });
//   });
//
// 行为：
//   - 按钮加 .btn-loading（CSS 给前面拼旋转 spinner）+ disabled
//   - 消息区加 .loading（旋转 spinner + 灰色 muted 文案）
//   - fn() 完成（resolve/reject 都行）后恢复按钮状态；消息区交回调方处理（成功改"已完成"、失败改错误信息）
//
// 设计取舍：
//   - 不强行 try/finally 接管错误——让调用方自己 catch，能区分"用户取消"vs"真失败"
//   - 不集中 console.log——调用方自己决定要不要 log
//   - 单一 helper 处理 button + statusEl，避免每个按钮各自重复 disabled/className 操作

export async function withLoading(button, statusEl, loadingMsg, fn) {
  if (button) {
    button.classList.add('btn-loading');
    button.disabled = true;
  }
  if (statusEl) {
    statusEl.classList.add('loading');
    statusEl.textContent = loadingMsg || '处理中…';
  }
  try {
    return await fn();
  } finally {
    if (button) {
      button.classList.remove('btn-loading');
      button.disabled = false;
    }
    if (statusEl) {
      statusEl.classList.remove('loading');
      // 不清 textContent——让调用方在 try 后写"成功/失败"消息
    }
  }
}

// setMsg 是配套的 helper：清掉 loading class 并填一段 plain 文本。
// 一般在 withLoading 的 fn 内部最后一行用，或者外部 try { ... .finally { setMsg } }。
export function setMsg(el, text) {
  if (!el) return;
  el.classList.remove('loading');
  el.textContent = text || '';
}
