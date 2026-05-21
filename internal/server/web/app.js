// oh-my-lan admin UI — vanilla JS，无依赖。
// 一份代码同时跑在：
//   1) omlserver embed.FS 提供的 /admin/（浏览器同源）
//   2) Tauri webview 加载的 tauri://localhost（跨源 → 必须用绝对 URL 调 server）
//
// Tauri 环境额外提供：服务器地址配置视图 + 本机 daemon 启停 tab。

const TOKEN_KEY = 'oml-admin-token';
const SERVER_URL_KEY = 'oml-server-url';
const ACTIVE_TAB_KEY = 'oml-admin-active-tab';
const CTL_PATH_KEY = 'oml-ctl-path';
const CTL_CONFIG_KEY = 'oml-ctl-config';

// Tauri 2.x 自身会注入只读 `window.isTauri`（boolean）—— 不能用 `isTauri` 当变量名
// 否则触发 "Can't create duplicate variable that shadows a global property"。
// 这里用 `inTauri`，并把 window.isTauri 作为最直接的探测信号。
const inTauri = !!(window.isTauri || window.__TAURI_INTERNALS__ || window.__TAURI__);
const tauriInvoke = inTauri
  ? (window.__TAURI__?.core?.invoke || window.__TAURI_INTERNALS__?.invoke)
  : null;

const els = {
  serverInfo: document.getElementById('server-info'),
  refreshBtn: document.getElementById('refresh-btn'),
  logoutBtn: document.getElementById('logout-btn'),
  issueTokenBtn: document.getElementById('issue-token-btn'),
  devicesIssueTokenBtn: document.getElementById('devices-issue-token-btn'),
  serverConfigView: document.getElementById('server-config-view'),
  serverConfigForm: document.getElementById('server-config-form'),
  serverUrlInput: document.getElementById('server-url-input'),
  serverConfigErr: document.getElementById('server-config-err'),
  loginView: document.getElementById('login-view'),
  loginForm: document.getElementById('login-form'),
  usernameInput: document.getElementById('username-input'),
  passwordInput: document.getElementById('password-input'),
  loginErr: document.getElementById('login-err'),
  currentServerDisplay: document.getElementById('current-server-display'),
  changeServerLink: document.getElementById('change-server-link'),
  mainView: document.getElementById('main-view'),
  tabs: document.querySelectorAll('.tab'),
  panels: {
    devices: document.getElementById('tab-devices'),
    services: document.getElementById('tab-services'),
    forwards: document.getElementById('tab-forwards'),
    audit: document.getElementById('tab-audit'),
    info: document.getElementById('tab-info'),
    local: document.getElementById('tab-local'),
  },
  bodies: {
    devices: document.getElementById('devices-tbody'),
    services: document.getElementById('services-tbody'),
    forwards: document.getElementById('forwards-tbody'),
    audit: document.getElementById('audit-tbody'),
  },
  infoDl: document.getElementById('info-dl'),
  metricsGrid: document.getElementById('metrics-grid'),
  serviceAddBtn: document.getElementById('service-add-btn'),
  forwardAddBtn: document.getElementById('forward-add-btn'),
  serviceModal: document.getElementById('service-modal'),
  serviceForm: document.getElementById('service-form'),
  forwardModal: document.getElementById('forward-modal'),
  forwardForm: document.getElementById('forward-form'),
  tokenModal: document.getElementById('token-modal'),
  issuedTokenDisplay: document.getElementById('issued-token-display'),
  issuedTokenExpires: document.getElementById('issued-token-expires'),
  copyTokenBtn: document.getElementById('copy-token-btn'),
  // 本机 daemon 面板（仅 Tauri）
  enrollCard: document.getElementById('enroll-card'),
  daemonCard: document.getElementById('daemon-card'),
  autostartCard: document.getElementById('autostart-card'),
  enrollForm: document.getElementById('enroll-form'),
  enrollServerDisplay: document.getElementById('enroll-server-display'),
  enrollNameInput: document.getElementById('enroll-name-input'),
  enrollTokenInput: document.getElementById('enroll-token-input'),
  enrollGenerateTokenBtn: document.getElementById('enroll-generate-token-btn'),
  enrollMsg: document.getElementById('enroll-msg'),
  autostartState: document.getElementById('autostart-state'),
  autostartEnableBtn: document.getElementById('autostart-enable-btn'),
  autostartDisableBtn: document.getElementById('autostart-disable-btn'),
  autostartMsg: document.getElementById('autostart-msg'),
  // 通用 alert 弹窗
  alertModal: document.getElementById('alert-modal'),
  alertTitle: document.getElementById('alert-title'),
  alertMessage: document.getElementById('alert-message'),
  alertOkBtn: document.getElementById('alert-ok-btn'),
  alertCancelBtn: document.getElementById('alert-cancel-btn'),
  ctlPathInput: document.getElementById('ctl-path-input'),
  ctlConfigInput: document.getElementById('ctl-config-input'),
  daemonStatusBadge: document.getElementById('daemon-status-badge'),
  daemonStartBtn: document.getElementById('daemon-start-btn'),
  daemonStopBtn: document.getElementById('daemon-stop-btn'),
  daemonRefreshBtn: document.getElementById('daemon-refresh-btn'),
  daemonReenrollBtn: document.getElementById('daemon-reenroll-btn'),
  daemonMsg: document.getElementById('daemon-msg'),
};

// --- 通用 alert/confirm 组件 ---
//
// 浏览器原生 window.alert 在 Tauri WKWebView 下样式僵硬、不可拖动、不支持中文 fallback 字体；
// 这里用一个 <dialog> 元素包装出 Promise 化的 showAlert / showConfirm。
// 设计为单例：同时只能有一个 alert 弹窗（多了会互相覆盖），调用方靠 await 串行。
let _alertResolver = null;
function showAlert(message, opts = {}) {
  const { title = '提示', kind = 'info', confirm = false } = opts;
  els.alertTitle.textContent = title;
  els.alertTitle.className = 'alert-title' + (kind === 'error' ? ' alert-error' : '');
  els.alertMessage.textContent = String(message);
  els.alertCancelBtn.hidden = !confirm;
  // 旧 resolver 还没结算？直接 resolve(false) 释放，避免悬挂 Promise
  if (_alertResolver) { _alertResolver(false); _alertResolver = null; }
  return new Promise((resolve) => {
    _alertResolver = resolve;
    els.alertModal.showModal();
  });
}
function showConfirm(message, opts = {}) {
  return showAlert(message, { ...opts, confirm: true });
}
function _resolveAlert(value) {
  els.alertModal.close();
  if (_alertResolver) {
    const r = _alertResolver;
    _alertResolver = null;
    r(value);
  }
}
els.alertOkBtn.addEventListener('click', () => _resolveAlert(true));
els.alertCancelBtn.addEventListener('click', () => _resolveAlert(false));
els.alertModal.addEventListener('cancel', () => _resolveAlert(false)); // Esc 键

const getToken = () => localStorage.getItem(TOKEN_KEY) || '';
const setToken = t => { if (t) localStorage.setItem(TOKEN_KEY, t); else localStorage.removeItem(TOKEN_KEY); };
const getServerUrl = () => {
  // 浏览器同源：返回空，让 fetch 用相对路径
  // Tauri webview：必须有完整 URL
  if (!inTauri) return '';
  return (localStorage.getItem(SERVER_URL_KEY) || '').replace(/\/+$/, '');
};
const setServerUrl = u => {
  if (u) localStorage.setItem(SERVER_URL_KEY, u.replace(/\/+$/, ''));
  else localStorage.removeItem(SERVER_URL_KEY);
};

async function api(path, options = {}) {
  const url = getServerUrl() + path;
  const resp = await fetch(url, {
    method: options.method || 'GET',
    headers: {
      'Authorization': 'Bearer ' + getToken(),
      ...(options.body ? { 'Content-Type': 'application/json' } : {}),
    },
    body: options.body ? JSON.stringify(options.body) : undefined,
  });
  if (resp.status === 401) throw new Error('UNAUTHORIZED');
  if (!resp.ok) {
    let msg = resp.statusText;
    try { const j = await resp.json(); if (j.error) msg = j.error; } catch (_) {}
    throw new Error(`HTTP ${resp.status}: ${msg}`);
  }
  if (resp.status === 204) return null;
  return resp.json();
}

const escapeHTML = s => String(s).replace(/[&<>"']/g, c => ({
  '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
}[c]));

function fmtTime(s) {
  if (!s) return '-';
  try { return new Date(s).toLocaleString(); } catch (_) { return s; }
}

// Triple-Code Rule: 颜色 + 文本 + 字符形状（[●] vs [○]）三轴同时编码。
// 历史教训：上一版只用了颜色 + 文本两轴，截图复制到纯文本上下文时区分不出在线/离线，
// 也没有兑现 DESIGN.md 第一条 Named Rule。
function statusBadge(status) {
  const online = status === 'online';
  const cls = online ? 'status-online' : 'status-offline';
  const glyph = online ? '●' : '○';
  const label = online ? '在线' : '离线';
  return `<span class="${cls}">${glyph} ${label}</span>`;
}

// enabled/disabled 的三轴等价：文本明确（"已启用"/"已停用"），weight 由 status 类承担。
// 不用 ✓/✗——那俩字符接近 emoji 装饰，与 deadpan tooling 不一致。
function enabledBadge(enabled) {
  const cls = enabled ? 'status-online' : 'status-offline';
  const glyph = enabled ? '●' : '○';
  const label = enabled ? '已启用' : '已停用';
  return `<span class="${cls}">${glyph} ${label}</span>`;
}

// 空态：除了说"没有"，要告诉用户"下一步去哪"。
// nextStep 是可选的 HTML 片段（已自行 escape），用 muted 文案展示。
function renderEmpty(tbody, cols, msg, nextStep) {
  const main = `<div class="empty-title">${escapeHTML(msg)}</div>`;
  const hint = nextStep ? `<div class="empty-hint">${nextStep}</div>` : '';
  tbody.innerHTML = `<tr><td colspan="${cols}" class="empty">${main}${hint}</td></tr>`;
}

// --- 数据加载（同 M5.2.X） ---

async function loadDevices() {
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
        <button class="row-btn btn-danger" data-action="revoke-device" data-id="${escapeHTML(d.id)}" data-name="${escapeHTML(d.name)}">撤销</button>
      </td>
    </tr>
  `).join('');
}

async function loadServices() {
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

async function loadForwards() {
  const data = await api('/api/admin/forwards');
  const tbody = els.bodies.forwards;
  if (!data.forwards || data.forwards.length === 0) {
    // 列数从原本 9 缩到 5（合并箭头列 + 公网端口列后剩 owner/route/protocol/enabled/actions）
    renderEmpty(tbody, 5, '尚无 forward 规则',
      '点击上方「+ 添加 forward」把别的设备的服务映射到本机端口；先确保「服务」tab 里有目标服务。');
    return;
  }
  // 把 "本地端口 → 远端服务@远端设备" 三列合并成一列：
  // 之前 → 单独占一列且无信息，浪费视觉。合并后映射关系一行可读，列数从 9 减到 7。
  tbody.innerHTML = data.forwards.map(f => {
    const route = `<span class="mono">${f.local_port}</span> → ` +
      `<span class="mono">${f.remote_public_port}</span> ` +
      `(${escapeHTML(f.remote_service_name)}@${escapeHTML(f.remote_device_name)})`;
    const remoteLabel = `${f.remote_service_name}@${f.remote_device_name}`;
    return `
    <tr>
      <td>${escapeHTML(f.owner_device_name)}</td>
      <td>${route}</td>
      <td>${escapeHTML(f.protocol)}</td>
      <td>${enabledBadge(f.enabled)}</td>
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

async function loadInfo() {
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
}

function fmtUptime(sec) {
  sec = Math.max(0, Number(sec) || 0);
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = sec % 60;
  if (d > 0) return `${d}d ${h}h ${m}m`;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m ${s}s`;
  return `${s}s`;
}

async function loadAudit() {
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

// --- 行内动作 ---

// 危险动作的影响面文案：三段式（操作 / 影响面 / 不可逆），用 \n 分行让 .alert-message
// 的 white-space: pre-wrap 自然展示——这是 DESIGN.md 第 4 条 Design Principle 的具象。
// 历史教训：之前这里走浏览器原生 confirm()，影响面一行铺平 + Tauri WKWebView 样式僵硬，
// 是整个 UI 唯一可能造成数据丢失的设计漏洞。改用 showConfirm() 与 alert 共用同一 dialog。
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

async function handleRowAction(action, data) {
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
    await refreshActive();
  } catch (e) {
    showAlert(e.message, { title: '操作失败', kind: 'error' });
  }
}

// --- Modals ---

async function openServiceModal() {
  const data = await api('/api/admin/devices');
  const sel = els.serviceForm.elements['device_id'];
  sel.innerHTML = '';
  (data.devices || []).forEach(d => {
    const opt = document.createElement('option');
    opt.value = d.id;
    opt.textContent = `${d.name} (${d.id.slice(0, 8)}…)`;
    sel.appendChild(opt);
  });
  els.serviceForm.reset();
  els.serviceModal.showModal();
}

async function openForwardModal() {
  const [devs, svcs] = await Promise.all([
    api('/api/admin/devices'),
    api('/api/admin/services'),
  ]);
  const devSel = els.forwardForm.elements['owner_device_id'];
  devSel.innerHTML = '';
  (devs.devices || []).forEach(d => {
    const opt = document.createElement('option');
    opt.value = d.id;
    opt.textContent = `${d.name}`;
    devSel.appendChild(opt);
  });
  const svcSel = els.forwardForm.elements['remote_service_id'];
  svcSel.innerHTML = '';
  (svcs.services || []).filter(s => s.enabled).forEach(s => {
    const opt = document.createElement('option');
    opt.value = s.id;
    opt.textContent = `${s.device_name}/${s.name} (${s.protocol} :${s.public_port})`;
    svcSel.appendChild(opt);
  });
  els.forwardForm.reset();
  els.forwardModal.showModal();
}

async function submitServiceForm(e) {
  e.preventDefault();
  const data = Object.fromEntries(new FormData(els.serviceForm).entries());
  try {
    await api('/api/admin/services', { method: 'POST', body: data });
    els.serviceModal.close();
    await refreshActive();
  } catch (err) { showAlert(err.message, { title: '发布服务失败', kind: 'error' }); }
}

async function submitForwardForm(e) {
  e.preventDefault();
  const data = Object.fromEntries(new FormData(els.forwardForm).entries());
  data.local_port = Number(data.local_port);
  try {
    await api('/api/admin/forwards', { method: 'POST', body: data });
    els.forwardModal.close();
    await refreshActive();
  } catch (err) { showAlert(err.message, { title: '添加 forward 失败', kind: 'error' }); }
}

async function issueToken() {
  try {
    const r = await api('/api/admin/enroll/tokens', { method: 'POST' });
    els.issuedTokenDisplay.textContent = r.token;
    els.issuedTokenExpires.textContent = fmtTime(r.expires_at);
    els.tokenModal.showModal();
  } catch (e) { showAlert(e.message, { title: '生成 token 失败', kind: 'error' }); }
}

// --- Tauri 本机 daemon 控制 ---

async function tauriCmd(name, args) {
  if (!tauriInvoke) throw new Error('IPC unavailable');
  return tauriInvoke(name, args || {});
}

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
//   1) 先判断 state.json 是否存在；不存在 → 显示注册卡片，daemon 控制卡片隐藏
//   2) 已注册 → 显示 daemon 控制卡片，并刷新 status
async function refreshLocalTab() {
  // placeholder 始终同步
  try {
    const defCfg = await tauriCmd('default_client_config_path_cmd');
    if (defCfg) els.ctlConfigInput.placeholder = `留空 → ${defCfg}`;
  } catch (e) {
    if (inTauri) console.warn('default_client_config_path_cmd 失败:', e);
  }

  const ctlPath = els.ctlPathInput.value.trim();
  const configPath = els.ctlConfigInput.value.trim();

  let enrolled = false;
  try {
    enrolled = await tauriCmd('daemon_is_enrolled', { ctlPath, configPath });
  } catch (e) {
    // 查不出来就保守认为未注册——让用户走一次注册总比卡死好
    console.warn('daemon_is_enrolled 失败:', e);
    enrolled = false;
  }

  if (!enrolled) {
    showEnrollCard();
  } else {
    showDaemonCard();
    await refreshDaemonStatus();
  }
}

function showEnrollCard() {
  els.enrollCard.hidden = false;
  els.daemonCard.hidden = true;
  if (els.autostartCard) els.autostartCard.hidden = true;
  els.enrollServerDisplay.value = getServerUrl() || '(请先在登录页配置服务器)';
  els.enrollMsg.textContent = '';
  // 默认聚焦在设备名（更可能让用户思考一下，token 后面贴即可）
  els.enrollNameInput.focus();
}

function showDaemonCard() {
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
  try {
    const s = await tauriCmd('daemon_status', { ctlPath, configPath });
    setDaemonBadge(s.running, s.pid);
    els.daemonMsg.textContent = '';
  } catch (e) {
    els.daemonMsg.textContent = '查询失败: ' + e;
    setDaemonBadge(false);
  }
  // status 拿到后，根据 autostart 状态再覆盖按钮可用性
  await refreshAutostart();
}

// 在登录态下调服务端 issue token，把结果写入注册表单的 token 输入框。
async function generateEnrollToken() {
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

// 把当前自启状态映射到三件 UI 上：状态徽章、开启按钮、关闭按钮
function applyAutostartUI({ supported, enabled }) {
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
    els.autostartMsg.textContent = 'daemon 由系统管理（launchd/systemd/VBS），UI 启停按钮已锁定';
    // autostart 开启时锁掉手动 start/stop——避免和 launchd/systemd 抢
    els.daemonStartBtn.disabled = true;
    els.daemonStopBtn.disabled = true;
  } else {
    els.autostartState.textContent = '未开启';
    els.autostartState.className = 'status-badge status-offline';
    els.autostartEnableBtn.disabled = false;
    els.autostartDisableBtn.disabled = true;  // 已是 disabled 状态，无需关闭
    els.autostartMsg.textContent = '';
  }
}

async function refreshAutostart() {
  try {
    const s = await tauriCmd('autostart_status');
    applyAutostartUI(s);
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
async function enableAutostart() {
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

async function disableAutostart() {
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
async function submitEnroll(ev) {
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

async function startDaemon() {
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

async function stopDaemon() {
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

// --- Tab 切换 ---

function activateTab(name) {
  // 本机 tab 仅 Tauri 可选；浏览器尝试激活时回退到 devices
  if (name === 'local' && !inTauri) name = 'devices';
  localStorage.setItem(ACTIVE_TAB_KEY, name);
  els.tabs.forEach(t => t.classList.toggle('active', t.dataset.tab === name));
  Object.entries(els.panels).forEach(([k, panel]) => { panel.hidden = k !== name; });
  refreshActive();
}

async function refreshActive() {
  const name = localStorage.getItem(ACTIVE_TAB_KEY) || 'devices';
  // loading 指示：在 active panel 上挂 aria-busy；CSS 用它给 .panel-actions 加一个 muted dot spinner，
  // 避免 fetch 期间用户看到上一次的 stale 数据却没有任何提示。
  const panel = els.panels[name];
  if (panel) panel.setAttribute('aria-busy', 'true');
  try {
    switch (name) {
      case 'devices':  await loadDevices(); break;
      case 'services': await loadServices(); break;
      case 'forwards': await loadForwards(); break;
      case 'audit':    await loadAudit(); break;
      case 'info':     await loadInfo(); break;
      case 'local':    await refreshLocalTab(); return;  // 本机 tab 不依赖远程
    }
    if (name !== 'info') loadInfo().catch(() => {});
  } catch (e) {
    if (e.message === 'UNAUTHORIZED') {
      setToken('');
      showLogin('会话已过期，请重新登录');
    } else {
      showAlert(e.message, { title: '加载失败', kind: 'error' });
    }
  } finally {
    if (panel) panel.removeAttribute('aria-busy');
  }
}

// --- 视图切换 ---

function hideAllViews() {
  els.serverConfigView.hidden = true;
  els.loginView.hidden = true;
  els.mainView.hidden = true;
  els.refreshBtn.hidden = true;
  els.logoutBtn.hidden = true;
  els.issueTokenBtn.hidden = true;
  els.serverInfo.textContent = '';
}

function showServerConfig(errMsg) {
  hideAllViews();
  els.serverConfigView.hidden = false;
  els.serverConfigErr.hidden = !errMsg;
  if (errMsg) els.serverConfigErr.textContent = errMsg;
  els.serverUrlInput.focus();
  els.serverUrlInput.value = getServerUrl() || '';
}

function showLogin(errMsg) {
  hideAllViews();
  els.loginView.hidden = false;
  els.loginErr.hidden = !errMsg;
  if (errMsg) els.loginErr.textContent = errMsg;
  if (inTauri) {
    els.currentServerDisplay.textContent = `当前服务器：${getServerUrl()}`;
    els.changeServerLink.hidden = false;
  }
  els.usernameInput.focus();
}

function showMain() {
  hideAllViews();
  els.mainView.hidden = false;
  els.refreshBtn.hidden = false;
  els.logoutBtn.hidden = false;
  els.issueTokenBtn.hidden = false;
  if (inTauri) {
    document.querySelector('.tab-tauri').hidden = false;
    // 恢复本机 daemon 控制的输入项
    els.ctlPathInput.value = localStorage.getItem(CTL_PATH_KEY) || '';
    els.ctlConfigInput.value = localStorage.getItem(CTL_CONFIG_KEY) || '';
  }
  const name = localStorage.getItem(ACTIVE_TAB_KEY) || 'devices';
  activateTab(name);
}

// --- 事件绑定 ---

els.serverConfigForm.addEventListener('submit', (e) => {
  e.preventDefault();
  const u = els.serverUrlInput.value.trim();
  if (!u) return;
  try {
    new URL(u);
  } catch (_) {
    els.serverConfigErr.textContent = 'URL 格式不合法';
    els.serverConfigErr.hidden = false;
    return;
  }
  setServerUrl(u);
  showLogin();
});

els.changeServerLink && els.changeServerLink.addEventListener('click', (e) => {
  e.preventDefault();
  setToken('');
  showServerConfig();
});

els.loginForm.addEventListener('submit', async (e) => {
  e.preventDefault();
  const u = els.usernameInput.value.trim();
  const p = els.passwordInput.value;
  if (!u || !p) return;

  setToken(''); // 清掉旧 session
  try {
    const url = getServerUrl() + '/api/auth/login';
    const resp = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: u, password: p }),
    });
    if (resp.status === 401) {
      els.loginErr.textContent = '用户名或密码错误';
      els.loginErr.hidden = false;
      return;
    }
    if (!resp.ok) {
      let msg = resp.statusText;
      try { const j = await resp.json(); if (j.error) msg = j.error; } catch (_) {}
      throw new Error(`HTTP ${resp.status}: ${msg}`);
    }
    const body = await resp.json();
    setToken(body.session_token);
    els.passwordInput.value = '';
    showMain();
  } catch (err) {
    let msg = err.message;
    if (inTauri && /Failed to fetch|NetworkError|TypeError/i.test(msg)) {
      msg = '连接失败：检查服务器 URL 是否正确（' + getServerUrl() + '）';
    }
    els.loginErr.textContent = msg;
    els.loginErr.hidden = false;
  }
});

els.tabs.forEach(t => t.addEventListener('click', () => activateTab(t.dataset.tab)));
els.refreshBtn.addEventListener('click', refreshActive);
els.logoutBtn.addEventListener('click', async () => {
    // 主动通知后端删 session（best-effort；删不掉也无所谓，反正客户端 token 已弃）
    try { await api('/api/auth/logout', { method: 'POST' }); } catch (_) {}
    setToken('');
    showLogin();
});
els.issueTokenBtn.addEventListener('click', issueToken);
// devices tab 的主操作入口，与 header 按钮指向同一行为
if (els.devicesIssueTokenBtn) els.devicesIssueTokenBtn.addEventListener('click', issueToken);
els.serviceAddBtn.addEventListener('click', () =>
  openServiceModal().catch(e => showAlert(e.message, { title: '打开失败', kind: 'error' })));
els.forwardAddBtn.addEventListener('click', () =>
  openForwardModal().catch(e => showAlert(e.message, { title: '打开失败', kind: 'error' })));
els.serviceForm.addEventListener('submit', submitServiceForm);
els.forwardForm.addEventListener('submit', submitForwardForm);

// Tauri 本机 daemon 控制按钮（如果元素不存在跳过）
if (inTauri && els.daemonStartBtn) {
  els.daemonStartBtn.addEventListener('click', startDaemon);
  els.daemonStopBtn.addEventListener('click', stopDaemon);
  els.daemonRefreshBtn.addEventListener('click', refreshLocalTab);
  els.daemonReenrollBtn.addEventListener('click', () => {
    // 用户主动想重置注册——不删除现有 state，只是切回注册卡片让他重新填
    showEnrollCard();
    els.enrollMsg.textContent = '注意：注册新身份会覆盖本机现有 state.json';
  });
  els.enrollForm.addEventListener('submit', submitEnroll);
  els.enrollGenerateTokenBtn.addEventListener('click', generateEnrollToken);
  els.autostartEnableBtn.addEventListener('click', enableAutostart);
  els.autostartDisableBtn.addEventListener('click', disableAutostart);
}

document.addEventListener('click', (e) => {
  const btn = e.target.closest('button[data-action]');
  if (btn) {
    handleRowAction(btn.dataset.action, btn.dataset);
    return;
  }
  const closeBtn = e.target.closest('button[data-close]');
  if (closeBtn) {
    const dlg = closeBtn.closest('dialog');
    if (dlg) dlg.close();
  }
});

els.copyTokenBtn.addEventListener('click', async () => {
  try {
    await navigator.clipboard.writeText(els.issuedTokenDisplay.textContent);
    els.copyTokenBtn.textContent = '已复制';
    setTimeout(() => { els.copyTokenBtn.textContent = '复制'; }, 1500);
  } catch (_) { showAlert('复制失败，请手动选中文本', { title: '剪贴板', kind: 'error' }); }
});

// --- 启动路径决策 ---

(function start() {
  if (inTauri && !getServerUrl()) {
    showServerConfig();
    return;
  }
  if (getToken()) {
    showMain();
  } else {
    showLogin();
  }
})();
