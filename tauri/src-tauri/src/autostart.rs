// 开机自启抽象：把 daemon 注册到平台原生服务管理器（不是注册 Tauri app 本身）。
//
//   * macOS  → launchd LaunchAgent（~/Library/LaunchAgents/com.oh-my-lan.daemon.plist）
//   * Linux  → systemd --user 单元（~/.config/systemd/user/oh-my-lan-daemon.service）
//   * Windows → 暂不支持，回返特定错误，前端把入口禁用并提示
//
// 设计取舍：
//   - 不引入 tauri-plugin-autostart：它注册的是 Tauri UI 自启动，不符合我们"daemon 永远在线、
//     UI 可有可无"的用例。
//   - 自启 daemon 用与手动启动**完全一致**的 `--config X --pid-file Y` 命令行，
//     这样 Tauri Rust 端的 pidfile 探活逻辑对两种来源透明。

use std::path::{Path, PathBuf};
use std::process::Command;

use serde::Serialize;

use crate::winhide::CommandHideWindow;

#[derive(Serialize, Debug, PartialEq, Eq)]
pub struct AutostartStatus {
    /// 当前平台是否支持开机自启
    pub supported: bool,
    /// 自启是否已开启（unit file / plist 存在并已 enable）
    pub enabled: bool,
    /// 自启 unit 文件的实际路径，前端只用于调试展示
    pub unit_path: Option<String>,
}

#[cfg(target_os = "macos")]
const PLIST_LABEL: &str = "com.oh-my-lan.daemon";

#[cfg(target_os = "macos")]
fn plist_path() -> Result<PathBuf, String> {
    let home = std::env::var("HOME").map_err(|_| "找不到 HOME".to_string())?;
    Ok(PathBuf::from(home)
        .join("Library")
        .join("LaunchAgents")
        .join(format!("{PLIST_LABEL}.plist")))
}

#[cfg(target_os = "windows")]
const WINDOWS_VBS_NAME: &str = "oh-my-lan-daemon.vbs";

#[cfg(target_os = "windows")]
fn windows_startup_vbs_path() -> Result<PathBuf, String> {
    // 用户登录后的 Startup 文件夹；放进去的快捷方式/脚本会在每次登录时执行一次。
    // %APPDATA% 总是用户级 Roaming，跨账号隔离干净；不需要管理员权限。
    let appdata = std::env::var("APPDATA").map_err(|_| "找不到 APPDATA".to_string())?;
    Ok(PathBuf::from(appdata)
        .join("Microsoft")
        .join("Windows")
        .join("Start Menu")
        .join("Programs")
        .join("Startup")
        .join(WINDOWS_VBS_NAME))
}

#[cfg(target_os = "linux")]
const SYSTEMD_UNIT_NAME: &str = "oh-my-lan-daemon.service";

#[cfg(target_os = "linux")]
fn systemd_unit_path() -> Result<PathBuf, String> {
    let base = if let Ok(xdg) = std::env::var("XDG_CONFIG_HOME") {
        PathBuf::from(xdg)
    } else {
        let home = std::env::var("HOME").map_err(|_| "找不到 HOME".to_string())?;
        PathBuf::from(home).join(".config")
    };
    Ok(base.join("systemd").join("user").join(SYSTEMD_UNIT_NAME))
}

/// 查询自启状态。任意平台调用都不报错——不支持就返回 supported=false。
pub fn status() -> Result<AutostartStatus, String> {
    #[cfg(target_os = "macos")]
    {
        let path = plist_path()?;
        let enabled = path.exists();
        return Ok(AutostartStatus {
            supported: true,
            enabled,
            unit_path: Some(path.to_string_lossy().into_owned()),
        });
    }
    #[cfg(target_os = "linux")]
    {
        let path = systemd_unit_path()?;
        // 文件存在仅说明 unit 写过；真正的"已 enable"应该问 systemctl is-enabled。
        // 对于"是否会开机启动"这层语义，is-enabled 才是权威；文件存在但 disable 也算关。
        let enabled_by_systemctl = Command::new("systemctl")
            .args(["--user", "is-enabled", SYSTEMD_UNIT_NAME])
            .hide_window()
            .output()
            .map(|o| o.status.success())
            .unwrap_or(false);
        return Ok(AutostartStatus {
            supported: true,
            enabled: enabled_by_systemctl,
            unit_path: Some(path.to_string_lossy().into_owned()),
        });
    }
    #[cfg(target_os = "windows")]
    {
        let path = windows_startup_vbs_path()?;
        let enabled = path.exists();
        return Ok(AutostartStatus {
            supported: true,
            enabled,
            unit_path: Some(path.to_string_lossy().into_owned()),
        });
    }
    #[cfg(not(any(target_os = "macos", target_os = "linux", target_os = "windows")))]
    {
        Ok(AutostartStatus {
            supported: false,
            enabled: false,
            unit_path: None,
        })
    }
}

/// 开启自启。`ctl`/`config`/`stderr`/`pid` 全用绝对路径写进 unit，避免相对路径在
/// launchd/systemd 的"PATH 很干净"环境下解析失败。
pub fn enable(ctl: &Path, config: &Path, stderr: &Path, pid_file: &Path) -> Result<(), String> {
    #[cfg(target_os = "macos")]
    {
        let plist = render_macos_plist(ctl, config, stderr, pid_file);
        let path = plist_path()?;
        if let Some(parent) = path.parent() {
            std::fs::create_dir_all(parent)
                .map_err(|e| format!("创建 LaunchAgents 目录失败: {e}"))?;
        }
        std::fs::write(&path, plist).map_err(|e| format!("写 plist {path:?}: {e}"))?;
        // launchctl load -w 会同时 enable + RunAtLoad 即时启动；旧版 macOS 也支持
        let out = Command::new("launchctl")
            .args(["load", "-w"])
            .arg(&path)
            .hide_window()
            .output()
            .map_err(|e| format!("调用 launchctl 失败: {e}"))?;
        if !out.status.success() {
            // launchctl load 在已加载时会返回非零；忽略 "already loaded" 这种重复操作
            let err = String::from_utf8_lossy(&out.stderr);
            if !err.contains("already loaded") && !err.contains("Operation already in progress") {
                return Err(format!("launchctl load 失败: {}", err.trim()));
            }
        }
        return Ok(());
    }
    #[cfg(target_os = "linux")]
    {
        let unit = render_systemd_unit(ctl, config, stderr, pid_file);
        let path = systemd_unit_path()?;
        if let Some(parent) = path.parent() {
            std::fs::create_dir_all(parent)
                .map_err(|e| format!("创建 systemd user 目录失败: {e}"))?;
        }
        std::fs::write(&path, unit).map_err(|e| format!("写 unit {path:?}: {e}"))?;
        // daemon-reload 让 systemd 看到新写入的 unit
        run_systemctl(&["--user", "daemon-reload"])?;
        run_systemctl(&["--user", "enable", "--now", SYSTEMD_UNIT_NAME])?;
        return Ok(());
    }
    #[cfg(target_os = "windows")]
    {
        let vbs_body = render_windows_vbs(ctl, config, stderr, pid_file);
        let path = windows_startup_vbs_path()?;
        if let Some(parent) = path.parent() {
            std::fs::create_dir_all(parent)
                .map_err(|e| format!("创建 Startup 目录失败: {e}"))?;
        }
        // VBS 必须保存为 UTF-16 LE with BOM。
        // 历史教训：UTF-8 注释含中文时，cscript/wscript 用系统 ANSI codepage（中文 Windows
        // 上是 GBK）解读，UTF-8 字节流被错解成乱码 → 注释行末尾被认为是"未闭合语句"
        // 续到下一行 → 把 `Set WshShell = ...` 吃掉 → 运行时报 "缺少对象 'WshShell'"。
        // BOM (0xFF 0xFE) 让 VBS 引擎切换到 UTF-16 解析路径，中文字符就能正确处理。
        let mut vbs_bytes: Vec<u8> = vec![0xFF, 0xFE]; // UTF-16 LE BOM
        for u in vbs_body.encode_utf16() {
            vbs_bytes.extend_from_slice(&u.to_le_bytes());
        }
        std::fs::write(&path, vbs_bytes).map_err(|e| format!("写 vbs {path:?}: {e}"))?;
        // VBS 在下次登录时才执行；为了"开启自启=立刻起一个 daemon"的直观语义，
        // 这里同步用 wscript.exe 启动一次。launchd/systemd 也是 enable + 立即启动同样的语义。
        let _ = Command::new("wscript.exe")
            .arg(&path)
            .hide_window()
            .spawn();
        return Ok(());
    }
    #[cfg(not(any(target_os = "macos", target_os = "linux", target_os = "windows")))]
    {
        let _ = (ctl, config, stderr, pid_file);
        Err("当前平台尚未实现开机自启；请手动用 任务计划程序 / 启动文件夹 配置".into())
    }
}

pub fn disable() -> Result<(), String> {
    #[cfg(target_os = "macos")]
    {
        let path = plist_path()?;
        if path.exists() {
            // unload -w 同时 disable + stop
            let out = Command::new("launchctl")
                .args(["unload", "-w"])
                .arg(&path)
                .hide_window()
                .output()
                .map_err(|e| format!("调用 launchctl 失败: {e}"))?;
            if !out.status.success() {
                let err = String::from_utf8_lossy(&out.stderr);
                // 没加载时 unload 会返回非零，忽略
                if !err.contains("Could not find specified service")
                    && !err.contains("Operation not permitted while System Integrity Protection")
                {
                    // 但仍然继续删除 plist 文件——下次启动就不会再加载
                    eprintln!("launchctl unload 警告: {}", err.trim());
                }
            }
            std::fs::remove_file(&path).map_err(|e| format!("删除 plist {path:?}: {e}"))?;
        }
        return Ok(());
    }
    #[cfg(target_os = "linux")]
    {
        let path = systemd_unit_path()?;
        // disable --now 同时 stop + 移除 wants 链接；忽略"未启用"错误
        let _ = run_systemctl(&["--user", "disable", "--now", SYSTEMD_UNIT_NAME]);
        if path.exists() {
            std::fs::remove_file(&path).map_err(|e| format!("删除 unit {path:?}: {e}"))?;
            let _ = run_systemctl(&["--user", "daemon-reload"]);
        }
        return Ok(());
    }
    #[cfg(target_os = "windows")]
    {
        let path = windows_startup_vbs_path()?;
        if path.exists() {
            std::fs::remove_file(&path).map_err(|e| format!("删除 vbs {path:?}: {e}"))?;
        }
        // 已启动的 daemon 不会被 VBS 控制（VBS 只负责拉起一次）。让上层 daemon_stop 走 pidfile
        // 路径去停它；这里只移除"下次登录时自动起"的钩子。
        return Ok(());
    }
    #[cfg(not(any(target_os = "macos", target_os = "linux", target_os = "windows")))]
    {
        Err("当前平台尚未实现开机自启".into())
    }
}

#[cfg(target_os = "linux")]
fn run_systemctl(args: &[&str]) -> Result<(), String> {
    let out = Command::new("systemctl")
        .args(args)
        .hide_window()
        .output()
        .map_err(|e| format!("调用 systemctl 失败: {e}"))?;
    if !out.status.success() {
        return Err(format!(
            "systemctl {args:?} 失败: {}",
            String::from_utf8_lossy(&out.stderr).trim()
        ));
    }
    Ok(())
}

#[cfg(target_os = "macos")]
pub fn render_macos_plist(ctl: &Path, config: &Path, stderr: &Path, pid_file: &Path) -> String {
    // 路径里如果出现 < > & " ' 需要 XML 转义；用户目录通常不会有，但用户名/磁盘卷名理论上可能
    let ctl = xml_escape(&ctl.to_string_lossy());
    let cfg = xml_escape(&config.to_string_lossy());
    let err = xml_escape(&stderr.to_string_lossy());
    let pid = xml_escape(&pid_file.to_string_lossy());
    format!(
        r#"<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>{PLIST_LABEL}</string>
  <key>ProgramArguments</key>
  <array>
    <string>{ctl}</string>
    <string>--config</string>
    <string>{cfg}</string>
    <string>daemon</string>
    <string>start</string>
    <string>--pid-file</string>
    <string>{pid}</string>
    <string>--log-file</string>
    <string>{err}</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardErrorPath</key>
  <string>{err}</string>
  <key>StandardOutPath</key>
  <string>{err}</string>
</dict>
</plist>
"#
    )
}

#[cfg(target_os = "linux")]
pub fn render_systemd_unit(ctl: &Path, config: &Path, stderr: &Path, pid_file: &Path) -> String {
    let ctl = ctl.to_string_lossy();
    let cfg = config.to_string_lossy();
    let err = stderr.to_string_lossy();
    let pid = pid_file.to_string_lossy();
    // 既用 --log-file 让 omlctl 自己落盘日志（与 Windows VBS 一致），又通过 StandardError
    // 把 systemd 接管的 stderr append 到同一文件——双保险，无论 daemon 哪一层先写都能到位
    format!(
        "[Unit]\n\
         Description=oh-my-lan client daemon (autostart)\n\
         After=network-online.target\n\
         \n\
         [Service]\n\
         Type=simple\n\
         ExecStart={ctl} --config {cfg} daemon start --pid-file {pid} --log-file {err}\n\
         Restart=on-failure\n\
         RestartSec=5\n\
         StandardError=append:{err}\n\
         StandardOutput=append:{err}\n\
         \n\
         [Install]\n\
         WantedBy=default.target\n"
    )
}

#[cfg(target_os = "windows")]
pub fn render_windows_vbs(ctl: &Path, config: &Path, stderr: &Path, pid_file: &Path) -> String {
    // VBS 在 Startup 文件夹里只会执行一次（用户登录时）。
    // 用 WshShell.Run 第 2 参数 = 0 隐藏窗口，第 3 参数 = False 不等待。
    //
    // 历史教训：上一版用 `cmd /c "<full inner command with embedded >> redirect>"` 包一层，
    // VBS 字符串转义 + cmd 元字符引号 双层嵌套，引号配对极易出错（实测错误码 800A0401
    // "语句未结束"）。重写为直接 WshShell.Run omlctl，stderr 重定向移到 omlctl 内部的
    // --log-file flag，引号层级减到最少。
    //
    // 配合 omlctl 自身在 Windows daemon 模式下 FreeConsole（cmd/omlctl/console_windows.go），
    // 任务栏闪现彻底消失。
    let ctl = vbs_escape(&ctl.to_string_lossy());
    let cfg = vbs_escape(&config.to_string_lossy());
    let err = vbs_escape(&stderr.to_string_lossy());
    let pid = vbs_escape(&pid_file.to_string_lossy());
    format!(
        "' Auto-generated by oh-my-lan desktop client.\r\n\
         ' 把本机 daemon 注册为开机自启（仅当前用户）。删除本文件即可关闭自启。\r\n\
         Set WshShell = CreateObject(\"WScript.Shell\")\r\n\
         cmd = \"\"\"{ctl}\"\" --config \"\"{cfg}\"\" daemon start --pid-file \"\"{pid}\"\" --log-file \"\"{err}\"\"\"\r\n\
         WshShell.Run cmd, 0, False\r\n"
    )
}

#[cfg(target_os = "windows")]
fn vbs_escape(s: &str) -> String {
    // VBS 字符串中只有 `"` 需要被加倍 `""`；其它字符直接放即可
    s.replace('"', "\"\"")
}

fn xml_escape(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    for ch in s.chars() {
        match ch {
            '&' => out.push_str("&amp;"),
            '<' => out.push_str("&lt;"),
            '>' => out.push_str("&gt;"),
            '"' => out.push_str("&quot;"),
            '\'' => out.push_str("&apos;"),
            _ => out.push(ch),
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn xml_escape_handles_metachars() {
        assert_eq!(
            xml_escape("a<b&c>\"d'e"),
            "a&lt;b&amp;c&gt;&quot;d&apos;e"
        );
    }

    #[test]
    fn xml_escape_passes_through_plain() {
        assert_eq!(xml_escape("/Users/alice/oh-my-lan"), "/Users/alice/oh-my-lan");
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn macos_plist_contains_all_arguments_in_order() {
        let plist = render_macos_plist(
            Path::new("/Applications/oh-my-lan.app/Contents/MacOS/omlctl"),
            Path::new("/Users/alice/Library/Application Support/oh-my-lan/client.yaml"),
            Path::new("/Users/alice/Library/Application Support/oh-my-lan/daemon.stderr"),
            Path::new("/Users/alice/Library/Application Support/oh-my-lan/daemon.pid"),
        );
        // 顺序很关键：cobra 不允许 flag 出现在 daemon start 之前以外的位置
        let ctl_pos = plist.find("omlctl").unwrap();
        let cfg_flag = plist.find("--config").unwrap();
        let cfg_val = plist.find("client.yaml").unwrap();
        // ">daemon<" 唯一匹配 `<string>daemon</string>` 关键字，避免错配到 Label "com.oh-my-lan.daemon"
        let daemon_kw = plist.find(">daemon<").unwrap();
        let start_kw = plist.find(">start<").unwrap();
        let pid_flag = plist.find("--pid-file").unwrap();
        let pid_val = plist.find("daemon.pid").unwrap();
        assert!(ctl_pos < cfg_flag);
        assert!(cfg_flag < cfg_val);
        assert!(cfg_val < daemon_kw);
        assert!(daemon_kw < start_kw);
        assert!(start_kw < pid_flag);
        assert!(pid_flag < pid_val);
        assert!(plist.contains("<key>RunAtLoad</key>"));
        assert!(plist.contains("<key>KeepAlive</key>"));
    }

    #[cfg(target_os = "windows")]
    #[test]
    fn windows_vbs_writes_utf16_le_bom() {
        // 模拟 autostart::enable 的写入路径，断言文件头是 UTF-16 LE BOM。
        // 这是为了防止再次回退到 UTF-8 + 中文注释组合（cscript 用 ANSI codepage
        // 误解读 → 注释末尾续行 → "缺少对象 'WshShell'" 运行时错误）。
        let vbs_body = render_windows_vbs(
            Path::new("C:\\oh-my-lan\\omlctl.exe"),
            Path::new("C:\\oh-my-lan\\client.yaml"),
            Path::new("C:\\oh-my-lan\\daemon.stderr"),
            Path::new("C:\\oh-my-lan\\daemon.pid"),
        );
        let mut vbs_bytes: Vec<u8> = vec![0xFF, 0xFE];
        for u in vbs_body.encode_utf16() {
            vbs_bytes.extend_from_slice(&u.to_le_bytes());
        }
        assert_eq!(&vbs_bytes[..2], &[0xFF, 0xFE], "must start with UTF-16 LE BOM");
        // 解码回 UTF-16 LE 应当能拿回原字符串
        let decoded: Vec<u16> = vbs_bytes[2..]
            .chunks_exact(2)
            .map(|c| u16::from_le_bytes([c[0], c[1]]))
            .collect();
        let roundtrip = String::from_utf16(&decoded).expect("valid UTF-16");
        assert_eq!(roundtrip, vbs_body);
    }

    #[cfg(target_os = "windows")]
    #[test]
    fn windows_vbs_contains_pid_file_and_hidden_window() {
        let vbs = render_windows_vbs(
            Path::new("C:\\Program Files\\oh-my-lan\\omlctl.exe"),
            Path::new("C:\\Users\\alice\\AppData\\Roaming\\oh-my-lan\\client.yaml"),
            Path::new("C:\\Users\\alice\\AppData\\Roaming\\oh-my-lan\\daemon.stderr"),
            Path::new("C:\\Users\\alice\\AppData\\Roaming\\oh-my-lan\\daemon.pid"),
        );
        // hidden window
        assert!(vbs.contains("WshShell.Run cmd, 0, False"));
        // 不再用 cmd /c 包层（历史教训：嵌套引号导致 800A0401 语句未结束）
        assert!(!vbs.contains("cmd /c"));
        // 顺序：omlctl → --config → daemon start → --pid-file → --log-file
        let daemon_idx = vbs.find("daemon start").unwrap();
        let pid_idx = vbs.find("--pid-file").unwrap();
        let log_idx = vbs.find("--log-file").unwrap();
        assert!(daemon_idx < pid_idx);
        assert!(pid_idx < log_idx);
        // CRLF line endings
        assert!(vbs.contains("\r\n"));
        // 引号闭合检查：cmd = "..." 一行的引号数必须为偶数（VBS 字符串字面量正确闭合）
        let cmd_line = vbs.lines().find(|l| l.starts_with("cmd =")).unwrap();
        let quote_count = cmd_line.chars().filter(|&c| c == '"').count();
        assert_eq!(quote_count % 2, 0, "VBS 引号数不是偶数，字符串未闭合: {cmd_line}");
    }

    #[test]
    fn vbs_escape_doubles_quotes() {
        #[cfg(target_os = "windows")]
        assert_eq!(vbs_escape(r#"a"b"c"#), r#"a""b""c"#);
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn linux_unit_has_install_section() {
        let unit = render_systemd_unit(
            Path::new("/usr/local/bin/omlctl"),
            Path::new("/home/alice/.config/oh-my-lan/client.yaml"),
            Path::new("/home/alice/.config/oh-my-lan/daemon.stderr"),
            Path::new("/home/alice/.config/oh-my-lan/daemon.pid"),
        );
        assert!(unit.contains("[Install]"));
        assert!(unit.contains("WantedBy=default.target"));
        assert!(unit.contains("ExecStart=/usr/local/bin/omlctl --config"));
        assert!(unit.contains("--pid-file /home/alice/.config/oh-my-lan/daemon.pid"));
    }
}
