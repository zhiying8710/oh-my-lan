// app/ssh.js — SSH 跳板信息卡片（仅浏览器同源模式渲染，桌面客户端不需要）。
//
// 数据来源：/api/admin/devices 返回的 AdminDeviceDTO，含 ssh_username 与 ssh_locked_at。
// 每行显示一台 device 的 SSH 跳板模板，点 "复制" 按钮 clipboard 抓走完整 ssh -L 命令。
//
// 复制的命令形如：
//   ssh -i ~/.config/oml/ssh_key -N -L <本机端口>:127.0.0.1:<public_port> oml-<id8>@<vps>
//
// 我们这里不知道 public_port（一台 device 可能有多个 service），所以模板里
// 让用户自己填——按钮给的是"骨架"。

import { els, escapeHTML, fmtTime, inTauri } from './core.js';
import { api } from './api.js';

// 当前 VPS host 缓存，从 /api/admin/info 拿一次
let _vpsHost = '';
let _vpsSSHPort = 22;

async function ensureVPSInfo() {
  if (_vpsHost) return;
  try {
    const info = await api('/api/admin/info');
    // chisel_addr 形如 "47.94.226.62:58443"，取 host 部分
    const m = (info.chisel_addr || '').match(/^([^:]+)/);
    _vpsHost = m ? m[1] : 'YOUR-VPS';
    // ssh_port 没在 AdminInfoResponse；先默认 22。后续可加字段
    _vpsSSHPort = 22;
  } catch (e) {
    _vpsHost = 'YOUR-VPS';
  }
}

export async function loadSSHKeys() {
  if (inTauri || !els.sshSection || !els.sshTbody) return;
  await ensureVPSInfo();
  els.sshSection.hidden = false;

  try {
    const data = await api('/api/admin/devices');
    if (!data.devices || data.devices.length === 0) {
      els.sshTbody.innerHTML = `<tr><td colspan="4" class="empty">尚无设备 enroll</td></tr>`;
      return;
    }
    els.sshTbody.innerHTML = data.devices.map(d => {
      const locked = d.ssh_locked_at;
      const status = locked
        ? `<span class="ssh-locked">已锁定<br><span class="mono">${fmtTime(locked)}</span></span>`
        : `<span class="ssh-ok">活跃</span>`;
      const user = d.ssh_username || '<未配置 SSH>';
      const tmpl = d.ssh_username
        ? `ssh -i &lt;私钥路径&gt; -N -L &lt;本机端口&gt;:127.0.0.1:&lt;public_port&gt; ${escapeHTML(d.ssh_username)}@${escapeHTML(_vpsHost)} -p ${_vpsSSHPort}`
        : '(此 device 无 SSH 跳板凭据)';
      const copyBtn = d.ssh_username
        ? `<button class="copy-ssh-btn" type="button" data-ssh-user="${escapeHTML(d.ssh_username)}">复制</button>`
        : '';
      return `<tr>
        <td>${escapeHTML(d.name)}<br><span class="mono">${escapeHTML(d.id.slice(0,8))}</span></td>
        <td class="mono">${escapeHTML(user)}</td>
        <td>${status}</td>
        <td><code class="mono">${tmpl}</code>${copyBtn}</td>
      </tr>`;
    }).join('');
  } catch (e) {
    els.sshTbody.innerHTML = `<tr><td colspan="4" class="empty">读 SSH 信息失败: ${escapeHTML(e.message || String(e))}</td></tr>`;
  }
}

// "复制 SSH 跳板命令" 按钮：装一次 delegated 监听器，给将来重渲染也生效
export function wireSSHCopyButton() {
  if (inTauri || !els.sshTbody) return;
  els.sshTbody.addEventListener('click', async (e) => {
    const btn = e.target.closest('.copy-ssh-btn');
    if (!btn) return;
    const user = btn.dataset.sshUser;
    const cmd = `ssh -i <私钥路径> -N -L <本机端口>:127.0.0.1:<public_port> ${user}@${_vpsHost} -p ${_vpsSSHPort}`;
    try {
      await navigator.clipboard.writeText(cmd);
      const orig = btn.textContent;
      btn.textContent = '已复制';
      setTimeout(() => { btn.textContent = orig; }, 1500);
    } catch (_) {
      // 旧浏览器或非 https：fallback select + execCommand
      btn.textContent = '复制失败';
    }
  });
}
