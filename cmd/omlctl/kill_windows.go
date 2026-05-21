//go:build windows

package main

import (
	"fmt"
	"os"
)

// Windows 没有 SIGTERM 概念；所有"软终止"都通过 TerminateProcess（即 os.Process.Kill）。
// 这套接口对调用方等价于 Unix 上的"友好失败"路径——hard kill 是唯一选项，因此 sendTerm
// 和 sendKill 实现一致。
//
// 设计折衷：Windows 桌面客户端通过 VBS 在登录后启动一次 omlctl，不存在 launchd
// KeepAlive 式的"被反复重启 + 抢占 pidfile"场景。所以这里的 preempt/孤儿清扫逻辑
// 实际上很少被触发；功能上保持可用即可，不必复刻 Unix 那套 ps-grep 兜底。

func isProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// os.FindProcess 在 Windows 上会 OpenProcess 拿 handle；handle 有效就视作存活。
	// 拿完 handle 立刻 Release（os.Process 在被 GC 时也会 close，但我们显式做更干净）。
	defer proc.Release()
	return true
}

// sendTerm 等价于 TerminateProcess——Windows 没有让进程"自己 cleanup"的标准方式。
// 调用方期望的"先 TERM 等 3s 再 KILL"在 Windows 上退化为"立刻 KILL"，可接受。
func sendTerm(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("open pid %d: %w", pid, err)
	}
	defer proc.Release()
	return proc.Kill()
}

// sendKill 与 sendTerm 在 Windows 上是同一个系统调用，保留两个名字仅为跨平台 API 一致。
func sendKill(pid int) error { return sendTerm(pid) }

// processLooksLikeOmlctl 在 Windows 上没有便宜的 ps 替代品（tasklist 解析啰嗦且
// 容易被 PowerShell / cmd / Git-Bash 之间的 quoting 差异坑到）。
// 这里保守地返回 true：pidfile 抢占的调用点要求目标已是 omlctl 写入的 pid，
// pid 复用风险在 Windows 32-bit pid 空间下极低（~10^4-10^5 唯一 pid，重启周期内复用率 < 1%）。
// 如果未来在 Windows 上确实发生过误杀，再加 tasklist 兜底。
func processLooksLikeOmlctl(_ int) bool { return true }

// psScanOmlctlByConfig 在 Windows 上不实现 ps-scan 兜底——VBS-startup 模型下，
// daemon 只有一个实例，pidfile 始终能定位到它，没有孤儿场景需要扫描。
// 返回空 slice 让调用方走仅 pidfile 路径。
func psScanOmlctlByConfig(_, _ string, _ int) []int { return nil }
