// local.js — Tauri 桌面客户端「本机」tab：注册卡片、daemon 启停、开机自启。
//
// 浏览器同源模式下此 tab 不存在（HTML 由 inTauri 标志条件渲染）；模块本身可以加载，
// 函数遇 !inTauri 直接 return / 触发空操作。

import { els, fmtTime, inTauri, CTL_PATH_KEY, CTL_CONFIG_KEY } from './core.js';
import { api } from './api.js';
import { tauriCmd, getServerUrl, ensureLocalDevice, clearLocalDeviceCache } from './state.js';
import { showAlert, showConfirm } from './alert.js';

function setDaemonBadge(running, pid) {
  if (running) {
    els.daemonStatusBadge.textContent = `运行中 · pid ${pid}`;
    els.daemonStatusBadge.className = 'status-badge status-online';
    els.daemonStartBtn.disabled = true;
    els.daemonStopBtn.disabled = false;
  } else {
    els.daemonStatusBadge.textContent = '已停止';
    els.daemonStatusBadge.className = 'status-badge status-offline';
    els.daemonStartBtn.disabled = false;
    els.daemonStopBtn.disabled = true;
  }
}

// refreshLocalTab 是「本机」tab 的统一入口：
//   1) 立即显示 loading 占位（Windows spawn 子进程慢，没有占位用户感觉白屏）
//   2) 并发拿三个 IPC：default_client_config_path、daemon_is_enrolled、autostart_status
//      这三个无依赖关系，可并行；总耗时 ≈ 最慢一个 vs 之前的串行总和
//   3) 全部回来后再决定显示哪张卡（enroll / daemon + autostart）
export async function refreshLocalTab() {
  showLoadingCard();

  // 三路并发——Promise.allSettled 让任一失败不阻塞另外两个
  const ctlPath = els.ctlPathInput.value.trim();
  const configPath = els.ctlConfigInput.value.trim();
  const [defCfgRes, enrolledRes] = await Promise.allSettled([
    tauriCmd('default_client_config_path_cmd'),
    tauriCmd('daemon_is_enrolled', { ctlPath, configPath }),
  ]);

  if (defCfgRes.status === 'fulfilled' && defCfgRes.value) {
    els.ctlConfigInput.placeholder = `留空 → ${defCfgRes.value}`;
  } else if (inTauri && defCfgRes.status === 'rejected') {
    console.warn('default_client_config_path_cmd 失败:', defCfgRes.reason);
  }

  const enrolled = enrolledRes.status === 'fulfilled' ? !!enrolledRes.value : false;
  if (enrolledRes.status === 'rejected') {
    console.warn('daemon_is_enrolled 失败:', enrolledRes.reason);
  }

  if (!enrolled) {
    clearLocalDeviceCache(); // 取消任何之前缓存的身份——未注册=没本机身份
    showEnrollCard();
  } else {
    // 已注册：warming the cache 让后续 openServiceModal / openForwardModal 不必再去 await
    ensureLocalDevice().catch(() => {});
    showDaemonCard();
    await refreshDaemonStatus();
  }
}

function showLoadingCard() {
  if (els.localLoadingCard) els.localLoadingCard.hidden = false;
  els.enrollCard.hidden = true;
  els.daemonCard.hidden = true;
  if (els.autostartCard) els.autostartCard.hidden = true;
}

export function showEnrollCard() {
  if (els.localLoadingCard) els.localLoadingCard.hidden = true;
  els.enrollCard.hidden = false;
  els.daemonCard.hidden = true;
  if (els.autostartCard) els.autostartCard.hidden = true;
  els.enrollServerDisplay.value = getServerUrl() || '(请先在登录页配置服务器)';
  els.enrollMsg.textContent = '';
  // 默认聚焦在设备名（更可能让用户思考一下，token 后面贴即可）
  els.enrollNameInput.focus();
}

function showDaemonCard() {
  if (els.localLoadingCard) els.localLoadingCard.hidden = true;
  els.enrollCard.hidden = true;
  els.daemonCard.hidden = false;
  // 注册之后 autostart 卡片也跟着出现，两张一并可见
  if (els.autostartCard) els.autostartCard.hidden = false;
}

async function refreshDaemonStatus() {
  // 查询前先把按钮都置 disabled，避免用户在不一致状态下连点
  els.daemonStartBtn.disabled = true;
  els.daemonStopBtn.disabled = true;
  els.daemonStatusBadge.textContent = '查询中…';
  els.daemonStatusBadge.className = 'status-badge';
  const ctlPath = els.ctlPathInput.value.trim();
  const configPath = els.ctlConfigInput.value.trim();
  let running = false;
  try {
    const s = await tauriCmd('daemon_status', { ctlPath, configPath });
    setDaemonBadge(s.running, s.pid);
    running = !!(s && s.running);
    els.daemonMsg.textContent = '';
  } catch (e) {
    els.daemonMsg.textContent = '查询失败: ' + e;
    setDaemonBadge(false);
  }
  // status 拿到后，把 daemon running 状态喂给 autostart UI 决策，
  // 让"autostart enabled + daemon 不在跑"场景下 start 按钮可点
  await refreshAutostart(running);
}

// 在登录态下调服务端 issue token，把结果写入注册表单的 token 输入框。
export async function generateEnrollToken() {
  els.enrollGenerateTokenBtn.disabled = true;
  try {
    const r = await api('/api/admin/enroll/tokens', { method: 'POST' });
    els.enrollTokenInput.value = r.token;
    els.enrollMsg.textContent = `已生成 token；过期时间：${fmtTime(r.expires_at)}`;
  } catch (e) {
    els.enrollMsg.textContent = '生成 token 失败: ' + e.message;
  } finally {
    els.enrollGenerateTokenBtn.disabled = false;
  }
}

// 把当前自启状态映射到三件 UI 上：状态徽章、开启按钮、关闭按钮、daemon 启停按钮可用性。
// daemonRunning 必填——决定"autostart 开启 + daemon 是否真在跑"两种子状态下的按钮锁。
function applyAutostartUI({ supported, enabled }, daemonRunning) {
  if (!supported) {
    els.autostartState.textContent = '不支持';
    els.autostartState.className = 'status-badge';
    els.autostartEnableBtn.disabled = true;
    els.autostartDisableBtn.disabled = true;
    els.autostartMsg.textContent = '当前平台暂未支持自动配置开机自启';
    return;
  }
  if (enabled) {
    els.autostartState.textContent = '已开启';
    els.autostartState.className = 'status-badge status-online';
    els.autostartEnableBtn.disabled = true;   // 已是 enabled 状态，不能再开启
    els.autostartDisableBtn.disabled = false;

    if (daemonRunning) {
      // 系统在管 + daemon 真跑着——锁掉手动按钮避免和 launchd/systemd/VBS 抢
      els.daemonStartBtn.disabled = true;
      els.daemonStopBtn.disabled = true;
      els.autostartMsg.textContent = 'daemon 由系统管理（launchd / systemd / VBS），UI 启停按钮已锁定';
    } else {
      // 自启开了但 daemon 不在跑：可能被外部杀死，或 launchd/VBS 还没拉起，
      // 或 Windows VBS 单次启动模型（不像 launchd 有 KeepAlive）daemon 崩了不会自动恢复。
      // 允许用户手动「启动 daemon」兜底，停止按钮仍然锁（没运行的进程没法停）。
      els.daemonStartBtn.disabled = false;
      els.daemonStopBtn.disabled = true;
      els.autostartMsg.textContent =
        '⚠ 自启已开启但 daemon 当前未运行——可能被外部杀死或自启拉起器尚未触发。点「启动 daemon」可手动恢复';
    }
  } else {
    els.autostartState.textContent = '未开启';
    els.autostartState.className = 'status-badge status-offline';
    els.autostartEnableBtn.disabled = false;
    els.autostartDisableBtn.disabled = true;  // 已是 disabled 状态，无需关闭
    els.autostartMsg.textContent = '';
    // autostart 关闭时不动 start/stop 按钮——已由 setDaemonBadge 按 daemon 实际状态设置过
  }
}

// daemonRunning 可省略：autostart enable/disable 按钮的回调里没有当前 daemon 状态上下文，
// 这种情况下会自己查一遍 daemon_status 拿状态。refreshDaemonStatus 路径走过来时已经知道，
// 显式传进来避免重复 IPC。
async function refreshAutostart(daemonRunning) {
  if (daemonRunning === undefined) {
    try {
      const ctlPath = els.ctlPathInput.value.trim();
      const configPath = els.ctlConfigInput.value.trim();
      const s = await tauriCmd('daemon_status', { ctlPath, configPath });
      daemonRunning = !!(s && s.running);
    } catch (_) {
      daemonRunning = false;
    }
  }
  try {
    const s = await tauriCmd('autostart_status');
    applyAutostartUI(s, daemonRunning);
  } catch (e) {
    els.autostartMsg.textContent = '查询自启失败: ' + e;
    els.autostartEnableBtn.disabled = true;
    els.autostartDisableBtn.disabled = true;
  }
}

// 轮询 daemon_status 直到 running=true 或超时。
// 用于："开启自启"之后 launchd/systemd 异步 spawn 出新 omlctl，要等它把 pidfile 写出来，
// UI 才能看到 running 状态——刚 await autostart_enable 完立刻 refresh 大概率撞上 race。
async function waitDaemonRunning(timeoutMs = 5000, intervalMs = 200) {
  const ctlPath = els.ctlPathInput.value.trim();
  const configPath = els.ctlConfigInput.value.trim();
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const s = await tauriCmd('daemon_status', { ctlPath, configPath });
      if (s && s.running) return s;
    } catch (_) { /* 继续轮询 */ }
    await new Promise(r => setTimeout(r, intervalMs));
  }
  return null;
}

// 真正打开自启：写 unit 文件 + 即时拉起一个 daemon。
// 开启前先把当前手动 daemon 全部清掉，避免被 launchd/systemd 拉起的新 daemon 抢走 pidfile，
// 旧 daemon 变孤儿无法被 UI 追踪（关闭自启时也无法清理）。
export async function enableAutostart() {
  const ctlPath = els.ctlPathInput.value.trim();
  const configPath = els.ctlConfigInput.value.trim();
  els.autostartEnableBtn.disabled = true;
  els.autostartDisableBtn.disabled = true;
  els.autostartMsg.textContent = '正在清理已有 daemon 进程…';
  try {
    await tauriCmd('daemon_kill_all', { ctlPath, configPath });
  } catch (e) {
    console.warn('开启自启前清理 daemon 失败:', e);
  }
  els.autostartMsg.textContent = '正在配置自启…';
  try {
    await tauriCmd('autostart_enable', { ctlPath, configPath });
  } catch (e) {
    await showAlert(String(e), { title: '开启自启失败', kind: 'error' });
    await refreshLocalTab();
    return;
  }
  // launchctl load / systemctl start --now 都是异步的——返回时 daemon 进程刚被排队 spawn，
  // pidfile 还没写。轮询等待至多 5s 让它真正起来，否则 UI 会瞬间显示"已停止"误导用户。
  els.autostartMsg.textContent = '正在等待 daemon 启动…';
  await waitDaemonRunning(5000, 200);
  await refreshLocalTab();
}

export async function disableAutostart() {
  const ok = await showConfirm(
    '将移除开机自启配置（plist / systemd unit / VBS），并清理所有 omlctl daemon 进程。\n要继续吗？',
    { title: '关闭开机自启' }
  );
  if (!ok) return;
  els.autostartEnableBtn.disabled = true;
  els.autostartDisableBtn.disabled = true;
  els.autostartMsg.textContent = '正在关闭自启…';
  const ctlPath = els.ctlPathInput.value.trim();
  const configPath = els.ctlConfigInput.value.trim();
  // 严格按"关闭 unit → 杀 pidfile 进程 → ps-grep 兜底杀孤儿"三步执行，每步错误都显示给用户。
  // 历史教训：早期版本静默 catch daemon_stop，结果有孤儿进程没被杀；UI 显示已停止但 ps 还能看到。
  try {
    await tauriCmd('autostart_disable');
  } catch (e) {
    await showAlert(String(e), { title: '关闭自启 unit 失败', kind: 'error' });
    await refreshLocalTab();
    return;
  }
  try {
    await tauriCmd('daemon_stop', { ctlPath, configPath });
  } catch (e) {
    // daemon_stop 失败不阻断流程，但要提示用户；后面 kill_all 也会兜底
    console.warn('daemon_stop 失败:', e);
  }
  // 兜底：扫描所有匹配 config 的 omlctl daemon 进程并 SIGTERM
  try {
    const msg = await tauriCmd('daemon_kill_all', { ctlPath, configPath });
    els.autostartMsg.textContent = msg || '已关闭自启';
  } catch (e) {
    await showAlert(String(e), { title: '清理孤儿进程失败', kind: 'error' });
  }
  // 等一拍再 refresh，让 SIGTERM/SIGKILL 完成
  await new Promise(r => setTimeout(r, 500));
  await refreshLocalTab();
}

// 注册并直接启动 daemon。出错时把 Rust 回传的 enroll stderr 摆出来。
export async function submitEnroll(ev) {
  ev.preventDefault();
  const ctlPath = els.ctlPathInput ? els.ctlPathInput.value.trim() : '';
  const configPath = els.ctlConfigInput ? els.ctlConfigInput.value.trim() : '';
  const serverUrl = getServerUrl();
  const deviceName = els.enrollNameInput.value.trim();
  const token = els.enrollTokenInput.value.trim();
  if (!serverUrl) {
    els.enrollMsg.textContent = '请先在登录页配置服务器 URL';
    return;
  }
  if (!deviceName || !token) {
    els.enrollMsg.textContent = '设备名和 token 都必填';
    return;
  }
  els.enrollMsg.textContent = '注册中…';
  try {
    const out = await tauriCmd('daemon_enroll', {
      ctlPath, configPath, serverUrl, token, deviceName,
    });
    els.enrollMsg.textContent = '注册成功，正在启动 daemon…';
    // enroll 成功后清空 token 框（一次性凭据，避免历史记录残留）
    els.enrollTokenInput.value = '';
    console.log('[enroll]', out);
    // 切到 daemon 卡片并启动
    showDaemonCard();
    await refreshDaemonStatus();
    await startDaemon();
  } catch (e) {
    els.enrollMsg.textContent = String(e);
  }
}

export async function startDaemon() {
  const ctlPath = els.ctlPathInput.value.trim();     // 留空 → Rust 用 .app 内置 sidecar
  const configPath = els.ctlConfigInput.value.trim(); // 留空 → Rust 用平台默认路径，并自动创建
  // 持久化用户当前输入（即便是空字符串也保存，表示"明确选了默认"）
  localStorage.setItem(CTL_PATH_KEY, ctlPath);
  localStorage.setItem(CTL_CONFIG_KEY, configPath);
  // 启动期间禁用按钮，避免并发触发
  els.daemonStartBtn.disabled = true;
  els.daemonStopBtn.disabled = true;
  els.daemonMsg.textContent = '启动中（含 500ms grace-check）…';
  try {
    const pid = await tauriCmd('daemon_start', { ctlPath, configPath });
    setDaemonBadge(true, pid);
    const notes = [
      ctlPath ? '' : '内置 omlctl',
      configPath ? '' : '默认 config',
    ].filter(Boolean).join(' + ');
    els.daemonMsg.textContent = `已启动 pid=${pid}${notes ? ' (' + notes + ')' : ''}`;
    // 即便 grace-check 通过，慢死的进程也可能 1~2s 后才 segfault；再补一次延迟刷新
    setTimeout(() => { refreshDaemonStatus().catch(() => {}); }, 1500);
  } catch (e) {
    // Rust 已经把 stderr 尾部拼进 e；多行错误用 <pre> 风格的换行显示
    els.daemonMsg.textContent = '启动失败：' + e;
    setDaemonBadge(false);
  }
}

export async function stopDaemon() {
  const ctlPath = els.ctlPathInput.value.trim();
  const configPath = els.ctlConfigInput.value.trim();
  try {
    await tauriCmd('daemon_stop', { ctlPath, configPath });
    els.daemonMsg.textContent = '已发送 SIGTERM';
    await refreshDaemonStatus();
  } catch (e) {
    els.daemonMsg.textContent = '停止失败: ' + e;
  }
}

