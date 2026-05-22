// state.js — 客户端运行时状态：token、server URL、本机身份缓存。
//
// 全部走 localStorage 持久化（无 cookies、无 sessionStorage）——Tauri WKWebView
// 关闭后 localStorage 会保留，下次启动免登录直到 session 过期。

import { TOKEN_KEY, SERVER_URL_KEY, inTauri, tauriInvoke, els } from './core.js';

export const getToken = () => localStorage.getItem(TOKEN_KEY) || '';
export const setToken = t => {
  if (t) localStorage.setItem(TOKEN_KEY, t);
  else localStorage.removeItem(TOKEN_KEY);
};

export const getServerUrl = () => {
  // 浏览器同源：返回空，让 fetch 用相对路径
  // Tauri webview：必须有完整 URL
  if (!inTauri) return '';
  return (localStorage.getItem(SERVER_URL_KEY) || '').replace(/\/+$/, '');
};

export const setServerUrl = u => {
  if (u) localStorage.setItem(SERVER_URL_KEY, u.replace(/\/+$/, ''));
  else localStorage.removeItem(SERVER_URL_KEY);
};

// 桌面客户端注册后从 state.json 拿到的本机身份，用来在「发布服务 / 添加 forward」
// 对话框里把 owner 锁成本机、并把 forward 的目标服务排除本机。
// 浏览器同源模式下 localDevice === null，对话框保留全部设备/服务（admin 视角）。
let localDevice = null; // { id: string, name: string } | null

export function clearLocalDeviceCache() {
  localDevice = null;
}

// 通用的 Tauri IPC 包装：浏览器侧调到这里直接抛错（调用方需先判 inTauri）。
export async function tauriCmd(name, args) {
  if (!tauriInvoke) throw new Error('IPC unavailable');
  return tauriInvoke(name, args || {});
}

// Tauri 环境下按需懒拉本机身份。每次调用会优先用 cache；cache 失效（未注册或读失败）
// 时再去 Rust 那边拉。失败回退到 null——对话框退化到 "可选全部设备"，行为与浏览器一致。
export async function ensureLocalDevice() {
  if (!inTauri) return null;
  if (localDevice && localDevice.id) return localDevice;
  try {
    const ctlPath = els.ctlPathInput.value.trim();
    const configPath = els.ctlConfigInput.value.trim();
    localDevice = await tauriCmd('daemon_local_device', { ctlPath, configPath });
  } catch (e) {
    console.warn('daemon_local_device 失败:', e);
    localDevice = null;
  }
  return localDevice;
}
