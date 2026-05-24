// core.js — 无依赖的"叶子模块"：localStorage key 常量、运行环境探测、
// 全局 DOM 句柄（els）、HTML escape、时间/在线徽章这类纯格式化助手。
//
// 拆分前所有内容都堆在 1232 行的 app.js 头部；现在按职责切开后，这里集中"几乎所有
// 其它模块都要 import 的基础设施"。新增格式化函数请加在本文件，不要再蔓延到 app.js。

export const TOKEN_KEY = 'oml-admin-token';
export const SERVER_URL_KEY = 'oml-server-url';
export const ACTIVE_TAB_KEY = 'oml-admin-active-tab';
export const CTL_PATH_KEY = 'oml-ctl-path';
export const CTL_CONFIG_KEY = 'oml-ctl-config';

// Tauri 2.x 自身会注入只读 `window.isTauri`（boolean）—— 不能用 `isTauri` 当变量名
// 否则触发 "Can't create duplicate variable that shadows a global property"。
// 这里用 `inTauri`，并把 window.isTauri 作为最直接的探测信号。
export const inTauri = !!(window.isTauri || window.__TAURI_INTERNALS__ || window.__TAURI__);
export const tauriInvoke = inTauri
  ? (window.__TAURI__?.core?.invoke || window.__TAURI_INTERNALS__?.invoke)
  : null;

// els 是全局 DOM 句柄字典，模块加载时一次性 getElementById。
// 使用 `<script type="module">`，HTML 解析完才执行，可直接拿到节点。
export const els = {
  serverInfo: document.getElementById('server-info'),
  refreshBtn: document.getElementById('refresh-btn'),
  logoutBtn: document.getElementById('logout-btn'),
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
  barkForm: document.getElementById('bark-form'),
  barkEnabled: document.getElementById('bark-enabled'),
  barkUrl: document.getElementById('bark-url'),
  barkThreshold: document.getElementById('bark-threshold'),
  barkTestBtn: document.getElementById('bark-test-btn'),
  barkMsg: document.getElementById('bark-msg'),
  logsPre: document.getElementById('logs-pre'),
  logsRefreshBtn: document.getElementById('logs-refresh-btn'),
  logsStatus: document.getElementById('logs-status'),
  // SSH 跳板信息（仅浏览器同源模式 unhide；桌面客户端不需要看）
  sshSection: document.getElementById('ssh-section'),
  sshTbody: document.getElementById('ssh-tbody'),
  // service 发布表单 bind_local 复选框
  svcBindLocal: document.getElementById('svc-bind-local'),
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
  localLoadingCard: document.getElementById('local-loading-card'),
  enrollForm: document.getElementById('enroll-form'),
  enrollServerDisplay: document.getElementById('enroll-server-display'),
  enrollNameInput: document.getElementById('enroll-name-input'),
  enrollTokenInput: document.getElementById('enroll-token-input'),
  enrollGenerateTokenBtn: document.getElementById('enroll-generate-token-btn'),
  enrollSubmitBtn: document.getElementById('enroll-submit-btn'),
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

export const escapeHTML = s => String(s).replace(/[&<>"']/g, c => ({
  '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
}[c]));

export function fmtTime(s) {
  if (!s) return '-';
  try { return new Date(s).toLocaleString(); } catch (_) { return s; }
}

export function fmtRelativeSince(d) {
  const diff = (Date.now() - d.getTime()) / 1000;
  if (diff < 60) return Math.round(diff) + 's 前';
  if (diff < 3600) return Math.round(diff / 60) + 'm 前';
  if (diff < 86400) return Math.round(diff / 3600) + 'h 前';
  return Math.round(diff / 86400) + 'd 前';
}

export function fmtUptime(sec) {
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

// Triple-Code Rule: 颜色 + 文本 + 字符形状（[●] vs [○]）三轴同时编码。
// 历史教训：上一版只用了颜色 + 文本两轴，截图复制到纯文本上下文时区分不出在线/离线，
// 也没有兑现 DESIGN.md 第一条 Named Rule。
export function statusBadge(status) {
  const online = status === 'online';
  const cls = online ? 'status-online' : 'status-offline';
  const glyph = online ? '●' : '○';
  const label = online ? '在线' : '离线';
  return `<span class="${cls}">${glyph} ${label}</span>`;
}

// enabled/disabled 的三轴等价：文本明确（"已启用"/"已停用"），weight 由 status 类承担。
// 不用 ✓/✗——那俩字符接近 emoji 装饰，与 deadpan tooling 不一致。
export function enabledBadge(enabled) {
  const cls = enabled ? 'status-online' : 'status-offline';
  const glyph = enabled ? '●' : '○';
  const label = enabled ? '已启用' : '已停用';
  return `<span class="${cls}">${glyph} ${label}</span>`;
}

// A1' 链路健康徽章：根据 service.last_probe_at / .last_probe_ok 显示。
// UDP service 当前 prober 只测 TCP（探测必失败），UI 显式标"UDP 不探测"避免误报。
export function linkHealthBadge(svc, protocol) {
  if (!svc || !svc.last_probe_at) {
    return `<span class="link-badge link-unknown">未探测</span>`;
  }
  if ((protocol || '').toLowerCase() === 'udp') {
    return `<span class="link-badge link-unknown" title="UDP 服务暂不参与 TCP 探测">UDP 不探测</span>`;
  }
  const probeTime = new Date(svc.last_probe_at);
  const rel = fmtRelativeSince(probeTime);
  if (svc.last_probe_ok) {
    return `<span class="link-badge link-ok" title="最近 TCP 探测成功">● ${rel}</span>`;
  }
  return `<span class="link-badge link-bad" title="最近 TCP 探测失败">○ ${rel}</span>`;
}

// 空态：除了说"没有"，要告诉用户"下一步去哪"。
// nextStep 是可选的 HTML 片段（已自行 escape），用 muted 文案展示。
export function renderEmpty(tbody, cols, msg, nextStep) {
  const main = `<div class="empty-title">${escapeHTML(msg)}</div>`;
  const hint = nextStep ? `<div class="empty-hint">${nextStep}</div>` : '';
  tbody.innerHTML = `<tr><td colspan="${cols}" class="empty">${main}${hint}</td></tr>`;
}
