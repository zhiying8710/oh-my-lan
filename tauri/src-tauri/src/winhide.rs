//! Windows 上 `std::process::Command::spawn`/`output`/`status` 默认会给 console-subsystem
//! 子进程分配一个 console（即使父进程是 GUI）。CREATE_NO_WINDOW 这个 creation flag 让
//! Windows 跳过 console 分配，进程仍然能跑、只是不弹黑窗。
//!
//! 我们所有调用 omlctl / launchctl / systemctl / wscript 的地方都要通过这个 trait
//! 链一下 `.hide_window()`。Unix 上是 no-op，编译时被 inline 掉。
//!
//! 不用 powershell start-process -windowstyle hidden 之类的间接方法：那需要拖入 PowerShell
//! 解释器（30+MB 启动），且引号嵌套又是噩梦。creation_flags 一次配齐最干净。

use std::process::Command;

pub trait CommandHideWindow {
    /// Windows 上设置 CREATE_NO_WINDOW；其它平台不变。
    /// 链式调用：`Command::new("foo").args(...).hide_window().spawn()`。
    fn hide_window(&mut self) -> &mut Self;
}

impl CommandHideWindow for Command {
    #[cfg(target_os = "windows")]
    fn hide_window(&mut self) -> &mut Self {
        use std::os::windows::process::CommandExt;
        // 0x0800_0000 = CREATE_NO_WINDOW（不为子进程分配 console）。
        // 不用 DETACHED_PROCESS (0x0000_0008)——后者会让子进程完全 detach 标准句柄，
        // 我们的 stderr/stdout 重定向到文件的逻辑就失效了。CREATE_NO_WINDOW 只是不弹黑窗，
        // 标准句柄行为不变，可被 redirect。
        const CREATE_NO_WINDOW: u32 = 0x0800_0000;
        self.creation_flags(CREATE_NO_WINDOW)
    }

    #[cfg(not(target_os = "windows"))]
    fn hide_window(&mut self) -> &mut Self {
        self
    }
}
