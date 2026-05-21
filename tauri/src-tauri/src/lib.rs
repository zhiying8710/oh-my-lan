// oh-my-lan desktop —— Tauri 2.x lib（标准模板布局）。
//
// IPC 命令薄薄一层包装在 daemon::DaemonManager 上：
//   * `daemon_start(ctl_path, config_path) -> u32`：spawn `omlctl daemon start`，返回 pid
//   * `daemon_stop()`：对已 spawn 的子进程发 SIGTERM（Unix）或 kill（Windows）
//   * `daemon_status() -> DaemonStatus`：检查子进程状态
//
// 真正的进程管理逻辑都在 daemon.rs，便于 cargo test 单测覆盖。

mod autostart;
mod daemon;

use std::sync::Arc;

#[allow(unused_imports)]
use tauri::Manager;
use tauri::State;

use daemon::{probe_pidfile, signal_terminate, DaemonManager, DaemonStatus};

/// Tauri State 包一层 Arc 是为了让多 IPC 调用共享同一个 DaemonManager。
#[derive(Default)]
struct AppState {
    daemon: Arc<DaemonManager>,
}

/// 解析 ctl_path / config_path，返回 (绝对路径 ctl, 绝对路径 cfg, stderr 路径, pid 文件路径)。
/// 多个 IPC 都需要这套衍生信息，集中在这里避免散落各处。
fn resolve_daemon_paths(
    ctl_path: Option<&str>,
    config_path: Option<&str>,
) -> Result<(String, std::path::PathBuf, std::path::PathBuf, std::path::PathBuf), String> {
    let resolved_ctl = match ctl_path {
        Some(s) if !s.trim().is_empty() => s.trim().to_string(),
        _ => default_ctl_path()?,
    };
    let resolved_cfg = match config_path {
        Some(s) if !s.trim().is_empty() => std::path::PathBuf::from(s.trim()),
        _ => default_client_config_path()?,
    };
    ensure_client_config(&resolved_cfg)?;
    let parent = resolved_cfg
        .parent()
        .map(|p| p.to_path_buf())
        .unwrap_or_else(|| std::path::PathBuf::from("."));
    let stderr_path = parent.join("daemon.stderr");
    let pid_path = parent.join("daemon.pid");
    Ok((resolved_ctl, resolved_cfg, stderr_path, pid_path))
}

#[tauri::command]
fn daemon_start(
    state: State<AppState>,
    ctl_path: Option<String>,
    config_path: Option<String>,
) -> Result<u32, String> {
    use std::time::Duration;
    let (resolved_ctl, resolved_cfg, stderr_path, pid_path) =
        resolve_daemon_paths(ctl_path.as_deref(), config_path.as_deref())?;

    // 启动前：如果 pidfile 指向一个仍存活的进程（比如开机自启拉起的 daemon），
    // 不要重复 spawn，直接报错让用户去关自启或者用 stop 复用 pidfile 流程。
    if let Some((pid, true)) = probe_pidfile(&pid_path) {
        return Err(format!(
            "daemon 已在运行 (pid={pid})，可能由开机自启拉起；请先停止或关闭开机自启"
        ));
    }

    let cfg_str = resolved_cfg.to_string_lossy().to_string();
    let pid_str = pid_path.to_string_lossy().to_string();
    state.daemon.start_with_diagnostics(
        &resolved_ctl,
        &[
            "--config", &cfg_str,
            "daemon", "start",
            "--pid-file", &pid_str,
        ],
        &stderr_path,
        Duration::from_millis(500),
    )
}

/// 找到与 Tauri 主进程同目录的 sidecar 二进制 `omlctl`（macOS .app/Contents/MacOS/、
/// Windows 同 install 目录、Linux 同前缀 bin 目录）。
/// dev 模式下也会指向 target/debug/omlctl。
fn default_ctl_path() -> Result<String, String> {
    let exe = std::env::current_exe().map_err(|e| format!("无法解析当前进程路径: {e}"))?;
    let dir = exe.parent().ok_or_else(|| "无父目录".to_string())?;
    let cand = dir.join(if cfg!(windows) { "omlctl.exe" } else { "omlctl" });
    Ok(cand.to_string_lossy().to_string())
}

/// 平台标准的 user-config 目录下的 `oh-my-lan/client.yaml`。
///   - macOS:   ~/Library/Application Support/oh-my-lan/client.yaml
///   - Linux:   $XDG_CONFIG_HOME/oh-my-lan/client.yaml  or  ~/.config/oh-my-lan/client.yaml
///   - Windows: %APPDATA%/oh-my-lan/client.yaml
fn default_client_config_path() -> Result<std::path::PathBuf, String> {
    let home = std::env::var("HOME").ok().or_else(|| std::env::var("USERPROFILE").ok());
    let appdata = std::env::var("APPDATA").ok();
    let xdg = std::env::var("XDG_CONFIG_HOME").ok();
    let os = if cfg!(target_os = "macos") {
        "macos"
    } else if cfg!(target_os = "windows") {
        "windows"
    } else {
        "linux"
    };
    resolve_client_config_path(os, home.as_deref(), appdata.as_deref(), xdg.as_deref())
}

/// Pure 版本：所有输入来自参数，便于单测覆盖 HOME / APPDATA / XDG 缺失等边界。
/// 不读任何全局 env。
fn resolve_client_config_path(
    target_os: &str,
    home: Option<&str>,
    appdata: Option<&str>,
    xdg_config_home: Option<&str>,
) -> Result<std::path::PathBuf, String> {
    use std::path::PathBuf;
    let base: PathBuf = match target_os {
        "macos" => {
            let h = home.ok_or_else(|| "找不到 HOME 环境变量".to_string())?;
            PathBuf::from(h).join("Library").join("Application Support").join("oh-my-lan")
        }
        "windows" => {
            if let Some(ad) = appdata {
                PathBuf::from(ad).join("oh-my-lan")
            } else {
                let h = home.ok_or_else(|| "找不到 APPDATA / USERPROFILE 环境变量".to_string())?;
                PathBuf::from(h).join("AppData").join("Roaming").join("oh-my-lan")
            }
        }
        _ => {
            // linux / 其它类 Unix
            if let Some(x) = xdg_config_home {
                PathBuf::from(x).join("oh-my-lan")
            } else {
                let h = home.ok_or_else(|| "找不到 HOME 环境变量".to_string())?;
                PathBuf::from(h).join(".config").join("oh-my-lan")
            }
        }
    };
    Ok(base.join("client.yaml"))
}

/// 如果 config 文件不存在，写一份合理默认值（data_dir = 与 config 同目录）。
/// 已存在的文件不动，保留用户原配置。
///
/// 用 `create_new(true)` 做原子创建：如果在 `exists()` 之后、`write()` 之前另一个
/// 进程已经写入，这里收到 `AlreadyExists` 错误，会被识别成"已被别人创建"并视为成功——
/// 避免覆盖用户首次启动后可能立即编辑过的配置。
fn ensure_client_config(path: &std::path::Path) -> Result<(), String> {
    use std::io::Write;
    let parent = path
        .parent()
        .ok_or_else(|| "config 路径没有父目录".to_string())?;
    std::fs::create_dir_all(parent).map_err(|e| format!("创建目录 {parent:?}: {e}"))?;

    let data_dir = parent.join("data");
    std::fs::create_dir_all(&data_dir).map_err(|e| format!("创建 data_dir {data_dir:?}: {e}"))?;

    // 用合理默认；YAML 写法保证跟 internal/config/client.go 兼容
    let body = format!(
        "# 由 oh-my-lan 桌面客户端首次启动时自动创建\n\
         data_dir: \"{}\"\n\
         reload_interval_seconds: 30\n\
         log:\n  level: info\n  format: text\n",
        data_dir.to_string_lossy().replace('\\', "/")
    );

    match std::fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(path)
    {
        Ok(mut f) => f
            .write_all(body.as_bytes())
            .map_err(|e| format!("写 {path:?}: {e}")),
        Err(e) if e.kind() == std::io::ErrorKind::AlreadyExists => Ok(()),
        Err(e) => Err(format!("打开 {path:?}: {e}")),
    }
}

#[tauri::command]
fn default_ctl_path_cmd() -> Result<String, String> {
    default_ctl_path()
}

#[tauri::command]
fn default_client_config_path_cmd() -> Result<String, String> {
    default_client_config_path().map(|p| p.to_string_lossy().to_string())
}

#[tauri::command]
fn daemon_stop(
    state: State<AppState>,
    ctl_path: Option<String>,
    config_path: Option<String>,
) -> Result<(), String> {
    use std::time::Duration;
    // 优先停内存里这把 spawn 的 child；若 Tauri 是重启后第一次见到 daemon（pid 在 pidfile 里
    // 但没有内存 Child），则走 SIGTERM-by-pid 路径。
    state.daemon.stop()?;

    // 即便上面成功，pidfile 也可能描述一个 launchd/systemd 拉起的 daemon。
    if let Ok((_, _, _, pid_path)) =
        resolve_daemon_paths(ctl_path.as_deref(), config_path.as_deref())
    {
        if let Some((pid, true)) = probe_pidfile(&pid_path) {
            if !signal_terminate(pid, Duration::from_secs(3)) {
                return Err(format!("发送 SIGTERM 给 pid {pid} 未能成功停止"));
            }
            // 让 omlctl 自己清理 pidfile；保险起见再删一次（已删则忽略）
            let _ = std::fs::remove_file(&pid_path);
        }
    }
    Ok(())
}

#[tauri::command]
fn daemon_status(
    state: State<AppState>,
    ctl_path: Option<String>,
    config_path: Option<String>,
) -> Result<DaemonStatus, String> {
    // 优先信任内存中的 Child（本 Tauri 进程 spawn 的）；它能区分 reap 状态。
    let in_mem = state.daemon.status()?;
    if in_mem.running {
        return Ok(in_mem);
    }
    // 否则查 pidfile——可能是开机自启拉起的，或 Tauri 上一次会话遗留的
    if let Ok((_, _, _, pid_path)) =
        resolve_daemon_paths(ctl_path.as_deref(), config_path.as_deref())
    {
        if let Some((pid, true)) = probe_pidfile(&pid_path) {
            return Ok(DaemonStatus { running: true, pid: Some(pid) });
        }
    }
    Ok(DaemonStatus { running: false, pid: None })
}

/// 调 `omlctl daemon kill` 兜底清扫——ps-grep 同 config 的所有 omlctl 进程。
/// 用于关闭自启时清理孤儿。返回 omlctl 的 stdout（含命中数）。
#[tauri::command]
fn daemon_kill_all(
    ctl_path: Option<String>,
    config_path: Option<String>,
) -> Result<String, String> {
    let (resolved_ctl, resolved_cfg, _, _) =
        resolve_daemon_paths(ctl_path.as_deref(), config_path.as_deref())?;
    let output = std::process::Command::new(&resolved_ctl)
        .args(["--config", &resolved_cfg.to_string_lossy(), "daemon", "kill"])
        .output()
        .map_err(|e| format!("调用 {resolved_ctl} daemon kill 失败: {e}"))?;
    if !output.status.success() {
        return Err(format!(
            "omlctl daemon kill 失败: {}",
            String::from_utf8_lossy(&output.stderr).trim()
        ));
    }
    Ok(String::from_utf8_lossy(&output.stdout).trim().to_string())
}

// ----------- 开机自启 IPC -----------

#[tauri::command]
fn autostart_status() -> Result<autostart::AutostartStatus, String> {
    autostart::status()
}

#[tauri::command]
fn autostart_enable(
    ctl_path: Option<String>,
    config_path: Option<String>,
) -> Result<(), String> {
    let (resolved_ctl, resolved_cfg, stderr_path, pid_path) =
        resolve_daemon_paths(ctl_path.as_deref(), config_path.as_deref())?;
    autostart::enable(
        std::path::Path::new(&resolved_ctl),
        &resolved_cfg,
        &stderr_path,
        &pid_path,
    )
}

#[tauri::command]
fn autostart_disable() -> Result<(), String> {
    autostart::disable()
}

/// 通过 `omlctl state exists` 同步判断本机是否已 enroll。
/// 返回 true → state.json 已存在；false → 未注册（不区分文件缺失 vs 权限等其它错误，
/// 走"当作未注册"是最稳健的兜底：让用户重新走一次 enroll 流程总好过卡住）。
#[tauri::command]
fn daemon_is_enrolled(
    ctl_path: Option<String>,
    config_path: Option<String>,
) -> Result<bool, String> {
    let resolved_ctl = match ctl_path.as_deref() {
        Some(s) if !s.trim().is_empty() => s.trim().to_string(),
        _ => default_ctl_path()?,
    };
    let resolved_cfg = match config_path.as_deref() {
        Some(s) if !s.trim().is_empty() => std::path::PathBuf::from(s.trim()),
        _ => default_client_config_path()?,
    };
    ensure_client_config(&resolved_cfg)?;

    let status = std::process::Command::new(&resolved_ctl)
        .args(["--config", &resolved_cfg.to_string_lossy(), "state", "exists"])
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null())
        .status()
        .map_err(|e| format!("调用 {resolved_ctl} 失败: {e}"))?;
    Ok(status.success())
}

/// 同步运行 `omlctl enroll`，把 stderr 捕获并在失败时回显给前端。
/// 完成后返回 stdout（包含 "注册成功：device_id=... name=..." 之类的成功消息）。
#[tauri::command]
fn daemon_enroll(
    ctl_path: Option<String>,
    config_path: Option<String>,
    server_url: String,
    token: String,
    device_name: String,
) -> Result<String, String> {
    let server_url = server_url.trim();
    let token = token.trim();
    let device_name = device_name.trim();
    if server_url.is_empty() || token.is_empty() || device_name.is_empty() {
        return Err("server_url / token / device_name 都不能为空".into());
    }

    let resolved_ctl = match ctl_path.as_deref() {
        Some(s) if !s.trim().is_empty() => s.trim().to_string(),
        _ => default_ctl_path()?,
    };
    let resolved_cfg = match config_path.as_deref() {
        Some(s) if !s.trim().is_empty() => std::path::PathBuf::from(s.trim()),
        _ => default_client_config_path()?,
    };
    ensure_client_config(&resolved_cfg)?;

    let output = std::process::Command::new(&resolved_ctl)
        .args([
            "--config",
            &resolved_cfg.to_string_lossy(),
            "enroll",
            "--server",
            server_url,
            "--token",
            token,
            "--name",
            device_name,
        ])
        .output()
        .map_err(|e| format!("调用 {resolved_ctl} enroll 失败: {e}"))?;

    if output.status.success() {
        Ok(String::from_utf8_lossy(&output.stdout).to_string())
    } else {
        let stderr = String::from_utf8_lossy(&output.stderr);
        let stdout = String::from_utf8_lossy(&output.stdout);
        // stderr 优先，stdout 兜底
        let detail = if !stderr.trim().is_empty() {
            stderr.into_owned()
        } else if !stdout.trim().is_empty() {
            stdout.into_owned()
        } else {
            format!("exit={}", output.status)
        };
        Err(format!("enroll 失败: {}", detail.trim()))
    }
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .setup(|app| {
            app.manage(AppState::default());
            // 保留 devtools 能力（Cargo.toml features = ["devtools"]）但不默认弹出。
            // 用户用 ⌘⌥I（macOS）/ Ctrl+Shift+I（Windows/Linux）即可手动打开 inspector。
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            daemon_start,
            daemon_stop,
            daemon_status,
            daemon_kill_all,
            daemon_is_enrolled,
            daemon_enroll,
            autostart_status,
            autostart_enable,
            autostart_disable,
            default_ctl_path_cmd,
            default_client_config_path_cmd
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::PathBuf;

    #[test]
    fn macos_uses_application_support() {
        let p = resolve_client_config_path("macos", Some("/Users/alice"), None, None).unwrap();
        assert_eq!(
            p,
            PathBuf::from("/Users/alice/Library/Application Support/oh-my-lan/client.yaml")
        );
    }

    #[test]
    fn macos_without_home_errors() {
        let err = resolve_client_config_path("macos", None, None, None).unwrap_err();
        assert!(err.contains("HOME"), "got: {err}");
    }

    #[test]
    fn windows_prefers_appdata_over_userprofile() {
        let p = resolve_client_config_path(
            "windows",
            Some("C:\\Users\\alice"),
            Some("C:\\Users\\alice\\AppData\\Roaming"),
            None,
        )
        .unwrap();
        assert_eq!(
            p,
            PathBuf::from("C:\\Users\\alice\\AppData\\Roaming/oh-my-lan/client.yaml")
        );
    }

    #[test]
    fn windows_falls_back_to_userprofile_appdata() {
        let p = resolve_client_config_path("windows", Some("C:\\Users\\alice"), None, None)
            .unwrap();
        assert_eq!(
            p,
            PathBuf::from("C:\\Users\\alice/AppData/Roaming/oh-my-lan/client.yaml")
        );
    }

    #[test]
    fn windows_without_anything_errors() {
        let err = resolve_client_config_path("windows", None, None, None).unwrap_err();
        assert!(err.contains("APPDATA") || err.contains("USERPROFILE"), "got: {err}");
    }

    #[test]
    fn linux_prefers_xdg_config_home() {
        let p = resolve_client_config_path(
            "linux",
            Some("/home/alice"),
            None,
            Some("/home/alice/.config-custom"),
        )
        .unwrap();
        assert_eq!(
            p,
            PathBuf::from("/home/alice/.config-custom/oh-my-lan/client.yaml")
        );
    }

    #[test]
    fn linux_falls_back_to_dot_config() {
        let p = resolve_client_config_path("linux", Some("/home/alice"), None, None).unwrap();
        assert_eq!(p, PathBuf::from("/home/alice/.config/oh-my-lan/client.yaml"));
    }

    #[test]
    fn linux_without_home_and_xdg_errors() {
        let err = resolve_client_config_path("linux", None, None, None).unwrap_err();
        assert!(err.contains("HOME"), "got: {err}");
    }

    #[test]
    fn unknown_os_falls_back_to_linux_logic() {
        // 对未识别的 target_os（如 freebsd / netbsd）走 linux 分支，行为可预测
        let p = resolve_client_config_path("freebsd", Some("/home/alice"), None, None).unwrap();
        assert_eq!(p, PathBuf::from("/home/alice/.config/oh-my-lan/client.yaml"));
    }

    // --- ensure_client_config ---

    #[test]
    fn ensure_client_config_creates_when_missing() {
        let tmp = std::env::temp_dir().join(format!("oml-ensure-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&tmp);
        let cfg = tmp.join("client.yaml");
        ensure_client_config(&cfg).unwrap();
        let body = std::fs::read_to_string(&cfg).unwrap();
        assert!(body.contains("data_dir"));
        assert!(tmp.join("data").is_dir());
        let _ = std::fs::remove_dir_all(&tmp);
    }

    #[test]
    fn ensure_client_config_keeps_existing_content() {
        let tmp = std::env::temp_dir().join(format!("oml-ensure-keep-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&tmp);
        std::fs::create_dir_all(&tmp).unwrap();
        let cfg = tmp.join("client.yaml");
        std::fs::write(&cfg, "# user-edited\ndata_dir: \"/custom\"\n").unwrap();
        ensure_client_config(&cfg).unwrap();
        let body = std::fs::read_to_string(&cfg).unwrap();
        assert!(body.contains("user-edited"), "原文件被覆盖了: {body}");
        let _ = std::fs::remove_dir_all(&tmp);
    }
}
