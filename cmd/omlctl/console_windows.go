//go:build windows

package main

import "syscall"

// detachConsoleIfNeeded 把当前进程从其 console 上解绑。
//
// 场景：Tauri Rust 或 VBS in Startup folder 这种 GUI 父进程 spawn omlctl 时，
// Windows 会自动为 console subsystem 的子进程分配一个 console 窗口。即使父进程把它
// 设为 hidden（WshShell.Run 第二参数 0），子进程的 console handle 本身仍然存在，
// 任务栏短暂闪一个黑窗。daemon 不需要 console（日志走 --log-file），早期 FreeConsole
// 让任务栏闪现彻底消失。
//
// 实现细节：用 syscall.NewLazyDLL/NewProc 直接调 kernel32!FreeConsole，避免拖入
// golang.org/x/sys/windows 这层额外依赖（FreeConsole 是个零参数的稳定 API，没必要包装）。
// 调用失败（进程本来就没 console）返回 0，无副作用；不检查返回值。
func detachConsoleIfNeeded() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	freeConsole := kernel32.NewProc("FreeConsole")
	_, _, _ = freeConsole.Call()
}
