// DaemonManager 把 omlctl 子进程的生命周期封装成一个可测试单元。
// Tauri IPC 命令（lib.rs 里 #[tauri::command]）只做参数解包并转发；
// 真正的 spawn / wait / kill 都在这里，能脱离 Tauri 框架被 cargo test 覆盖。

use std::path::Path;
use std::process::{Child, Command, Stdio};
use std::sync::Mutex;
use std::time::{Duration, Instant};

use serde::Serialize;

#[derive(Default)]
pub struct DaemonManager {
    child: Mutex<Option<Child>>,
}

#[derive(Serialize, Debug, PartialEq, Eq)]
pub struct DaemonStatus {
    pub running: bool,
    pub pid: Option<u32>,
}

impl DaemonManager {
    // 公开 ctor 给测试与未来调用方使用；lib.rs 通过 Default 间接构造。
    #[allow(dead_code)]
    pub fn new() -> Self {
        Self::default()
    }

    /// 启动任意命令。program + args 完整描述要 exec 的进程。
    /// 已有子进程在跑时返回 Err 以避免重复 spawn。
    ///
    /// 测试用的轻量版本：stdout/stderr 全部 null。`grace` 为 0 → 不做早死检测。
    /// 生产代码统一走 `start_with_diagnostics`；保留这个 API 让 cargo test 不必带文件依赖。
    #[allow(dead_code)]
    pub fn start(&self, program: &str, args: &[&str]) -> Result<u32, String> {
        self.start_inner(program, args, None, Duration::ZERO)
    }

    /// 生产用版本：把 stderr 重定向到 `stderr_path`（覆盖写），spawn 后等待 `grace`
    /// 检测子进程是否立刻死掉；若死了，读取该文件末尾内容拼进错误返回——避免出现
    /// "前端显示 pid X，下一秒 pid X 没了，但用户不知道为什么死的"这种 UX 黑洞。
    pub fn start_with_diagnostics(
        &self,
        program: &str,
        args: &[&str],
        stderr_path: &Path,
        grace: Duration,
    ) -> Result<u32, String> {
        self.start_inner(program, args, Some(stderr_path), grace)
    }

    fn start_inner(
        &self,
        program: &str,
        args: &[&str],
        stderr_path: Option<&Path>,
        grace: Duration,
    ) -> Result<u32, String> {
        let mut guard = self.child.lock().map_err(|e| format!("mutex poisoned: {e}"))?;

        if let Some(child) = guard.as_mut() {
            match child.try_wait() {
                Ok(Some(_)) => {
                    *guard = None;
                }
                Ok(None) => return Err(format!("daemon 已在运行 (pid={})", child.id())),
                Err(e) => return Err(format!("检测既有进程失败: {e}")),
            }
        }

        let stderr_file = if let Some(p) = stderr_path {
            if let Some(parent) = p.parent() {
                std::fs::create_dir_all(parent)
                    .map_err(|e| format!("创建 stderr 父目录 {parent:?}: {e}"))?;
            }
            let f = std::fs::OpenOptions::new()
                .create(true)
                .write(true)
                .truncate(true)
                .open(p)
                .map_err(|e| format!("打开 stderr 文件 {p:?}: {e}"))?;
            Some(f)
        } else {
            None
        };

        let stderr_stdio = match stderr_file {
            Some(f) => Stdio::from(f),
            None => Stdio::null(),
        };

        let mut child = Command::new(program)
            .args(args)
            .stdout(Stdio::null())
            .stderr(stderr_stdio)
            .spawn()
            .map_err(|e| format!("spawn {program} 失败: {e}"))?;

        let pid = child.id();

        // grace-wait：进程若在这段时间内直接退出，认为是配置/参数/enrollment 问题，
        // 把 stderr 的最后内容拼进错误返回，避免静默"pid 一闪即逝"。
        if grace > Duration::ZERO {
            let deadline = Instant::now() + grace;
            loop {
                match child.try_wait() {
                    Ok(Some(status)) => {
                        let tail = stderr_path
                            .and_then(|p| read_tail(p, 2048).ok())
                            .unwrap_or_default();
                        let msg = if tail.is_empty() {
                            format!("daemon 启动后立刻退出（exit={status}），且未捕获到 stderr——可能是 omlctl 路径不可执行")
                        } else {
                            format!("daemon 启动后立刻退出（exit={status}）。stderr 尾部：\n{tail}")
                        };
                        return Err(msg);
                    }
                    Ok(None) => {
                        if Instant::now() >= deadline {
                            break;
                        }
                        std::thread::sleep(Duration::from_millis(50));
                    }
                    Err(e) => return Err(format!("grace-check try_wait 失败: {e}")),
                }
            }
        }

        *guard = Some(child);
        Ok(pid)
    }

    /// Unix 发 SIGTERM 等 3s；超时 SIGKILL。Windows 直接 kill。
    pub fn stop(&self) -> Result<(), String> {
        let mut guard = self.child.lock().map_err(|e| format!("mutex poisoned: {e}"))?;
        if let Some(mut child) = guard.take() {
            terminate(&mut child);
            let _ = child.wait();
        }
        Ok(())
    }

    pub fn status(&self) -> Result<DaemonStatus, String> {
        let mut guard = self.child.lock().map_err(|e| format!("mutex poisoned: {e}"))?;
        let Some(child) = guard.as_mut() else {
            return Ok(DaemonStatus {
                running: false,
                pid: None,
            });
        };
        match child.try_wait() {
            Ok(None) => Ok(DaemonStatus {
                running: true,
                pid: Some(child.id()),
            }),
            _ => {
                *guard = None;
                Ok(DaemonStatus {
                    running: false,
                    pid: None,
                })
            }
        }
    }
}

/// 读 pidfile，返回 (pid, alive)。文件不存在或解析失败返回 None。
/// alive 用 `kill(pid, 0)` 探活：Unix 走 libc::kill；Windows 走 OpenProcess。
pub fn probe_pidfile(path: &Path) -> Option<(u32, bool)> {
    let raw = std::fs::read_to_string(path).ok()?;
    let pid: u32 = raw.trim().parse().ok()?;
    Some((pid, pid_alive(pid)))
}

#[cfg(unix)]
fn pid_alive(pid: u32) -> bool {
    // signal=0 不真的发信号，仅做权限+存活检测；ESRCH=不存在；EPERM=存在但无权限（仍算活）
    let rc = unsafe { libc::kill(pid as libc::pid_t, 0) };
    if rc == 0 {
        return true;
    }
    // errno
    let errno = std::io::Error::last_os_error().raw_os_error().unwrap_or(0);
    errno == libc::EPERM
}

#[cfg(not(unix))]
fn pid_alive(_pid: u32) -> bool {
    // Windows: 这次先简化为返回 false——pidfile 路径主要用于本机 daemon 探活，
    // Windows 实测延后；用户开机自启功能在 Windows 上也直接返回 unimplemented。
    false
}

/// 向给定 pid 发 SIGTERM，等待 timeout 后若仍存活发 SIGKILL。返回是否成功停止。
#[cfg(unix)]
pub fn signal_terminate(pid: u32, timeout: Duration) -> bool {
    unsafe { libc::kill(pid as libc::pid_t, libc::SIGTERM) };
    let deadline = Instant::now() + timeout;
    while Instant::now() < deadline {
        if !pid_alive(pid) {
            return true;
        }
        std::thread::sleep(Duration::from_millis(50));
    }
    unsafe { libc::kill(pid as libc::pid_t, libc::SIGKILL) };
    std::thread::sleep(Duration::from_millis(100));
    !pid_alive(pid)
}

#[cfg(not(unix))]
pub fn signal_terminate(_pid: u32, _timeout: Duration) -> bool {
    false
}

/// 读文件末尾最多 `max_bytes` 字节，按 UTF-8 lossy 解码。
/// 失败返回 Err；空文件返回 Ok("")。用于把 omlctl 的死亡现场摆给用户看。
fn read_tail(path: &Path, max_bytes: u64) -> std::io::Result<String> {
    use std::io::{Read, Seek, SeekFrom};
    let mut f = std::fs::File::open(path)?;
    let len = f.metadata()?.len();
    let start = len.saturating_sub(max_bytes);
    f.seek(SeekFrom::Start(start))?;
    let mut buf = Vec::with_capacity((len - start) as usize);
    f.read_to_end(&mut buf)?;
    Ok(String::from_utf8_lossy(&buf).into_owned())
}

#[cfg(unix)]
fn terminate(child: &mut Child) {
    let pid = child.id() as libc::pid_t;
    unsafe {
        libc::kill(pid, libc::SIGTERM);
    }
    let deadline = Instant::now() + Duration::from_secs(3);
    while Instant::now() < deadline {
        if let Ok(Some(_)) = child.try_wait() {
            return;
        }
        std::thread::sleep(Duration::from_millis(50));
    }
    let _ = child.kill();
}

#[cfg(not(unix))]
fn terminate(child: &mut Child) {
    let _ = child.kill();
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::thread::sleep;
    use std::time::Duration;

    fn sleep_bin() -> &'static str {
        // 几乎所有 Unix 都有 /bin/sleep；Windows 测试不在这条路径上跑。
        "/bin/sleep"
    }

    #[test]
    fn happy_path_lifecycle() {
        let mgr = DaemonManager::new();

        // 初始：未运行
        assert_eq!(
            mgr.status().unwrap(),
            DaemonStatus { running: false, pid: None }
        );

        // 启动 sleep 60 当 fake daemon
        let pid = mgr.start(sleep_bin(), &["60"]).expect("start should succeed");
        assert!(pid > 0);

        // 运行中状态
        let s = mgr.status().unwrap();
        assert!(s.running);
        assert_eq!(s.pid, Some(pid));

        // 二次 start 应该失败
        let err = mgr.start(sleep_bin(), &["60"]).unwrap_err();
        assert!(err.contains("已在运行"), "got: {err}");

        // stop → 状态变 false
        mgr.stop().unwrap();
        let s = mgr.status().unwrap();
        assert!(!s.running);
        assert!(s.pid.is_none());
    }

    #[test]
    fn restart_after_natural_exit() {
        let mgr = DaemonManager::new();
        // sleep 0.1：子进程会自然退出
        mgr.start(sleep_bin(), &["0.1"]).unwrap();
        // 等它退
        sleep(Duration::from_millis(300));

        // status() 会清掉已退出的 child，允许再次 start
        let s = mgr.status().unwrap();
        assert!(!s.running);

        // 重启应该成功（不应报 "已在运行"）
        let _pid = mgr.start(sleep_bin(), &["60"]).expect("restart should succeed");
        mgr.stop().unwrap();
    }

    #[test]
    fn start_with_nonexistent_program() {
        let mgr = DaemonManager::new();
        let err = mgr.start("/no/such/binary", &[]).unwrap_err();
        assert!(err.contains("spawn") || err.contains("No such"));
    }

    #[test]
    fn stop_when_not_running_is_noop() {
        let mgr = DaemonManager::new();
        mgr.stop().expect("stop on empty should not error");
    }
}
