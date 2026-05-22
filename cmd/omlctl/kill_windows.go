//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
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

// processLooksLikeOmlctl 用 `tasklist /fi "PID eq <pid>" /fo csv /nh /v` 验证 pid
// 对应进程的 image name 是不是 omlctl.exe。tasklist 全 Windows 平台原生（XP+），
// 不需要 PowerShell 启动成本（PowerShell 启动一次 ~500ms，tasklist <50ms）。
// 失败时保守返回 false，宁可不杀也别误杀。
func processLooksLikeOmlctl(pid int) bool {
	cmd := exec.Command("tasklist", "/fi", fmt.Sprintf("PID eq %d", pid), "/fo", "csv", "/nh")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x0800_0000}
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	// 输出第 1 列是 image name，被引号包裹："omlctl.exe","12345",...
	return strings.Contains(strings.ToLower(string(out)), "omlctl")
}

// psScanOmlctlByConfig 列出所有命令行匹配本 daemon 的 omlctl 进程。
//
// 实现优先级（按现代 Windows 推荐做法 + 性能权衡）：
//   1. PowerShell + Get-CimInstance：Win10+ 一直可用，Win11 22H2 起是唯一选项
//      （wmic 已被微软标记 deprecated 并默认不再随系统安装，
//      参考 https://learn.microsoft.com/en-us/windows/deprecated-features）。
//      启动开销 ~500ms。
//   2. wmic 兜底：老 Win10 系统 PowerShell 缺失或被禁用时仍能跑。
//
// 与 Unix 版相同语义：返回所有命令行同时包含 omlctl + daemon start + (configPath 或 abs)
// 的 pid；排除自己。失败回退空 slice，调用方走仅 pidfile 路径。
func psScanOmlctlByConfig(configPath, absPath string, self int) []int {
	// PowerShell 优先
	if pids := scanViaPowerShell(configPath, absPath, self); pids != nil {
		return pids
	}
	// 兜底走 wmic（Win10 早期版本 / 企业镜像剥掉 PowerShell 时仍可用）
	return scanViaWmic(configPath, absPath, self)
}

func hiddenSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x0800_0000}
}

// scanViaPowerShell 用 Get-CimInstance Win32_Process 列 omlctl.exe 的 pid+commandline。
// 输出每行 `pid\t<commandline>`，避免 csv 解析时被 quote/comma 坑（命令行常含路径 + 引号）。
// 返回 nil 表示 PowerShell 不可用（caller 应试 wmic）。
func scanViaPowerShell(configPath, absPath string, self int) []int {
	// -NoProfile 跳过用户 profile，缩短启动时间；ConsoleHost on Win11
	psScript := `Get-CimInstance Win32_Process -Filter "Name='omlctl.exe'" | ` +
		`ForEach-Object { "$($_.ProcessId)` + "`t" + `$($_.CommandLine)" }`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", psScript)
	cmd.SysProcAttr = hiddenSysProcAttr()
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseScannerOutput(string(out), "\t", configPath, absPath, self)
}

// scanViaWmic 兜底实现：调 wmic process where name='omlctl.exe' get pid+commandline，csv 格式。
func scanViaWmic(configPath, absPath string, self int) []int {
	cmd := exec.Command("wmic", "process", "where", "name='omlctl.exe'",
		"get", "processid,commandline", "/format:csv")
	cmd.SysProcAttr = hiddenSysProcAttr()
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	// wmic csv: Node,CommandLine,ProcessId —— 末尾字段是 pid，前面是 commandline
	// 把每行末尾 `,<pid>` 重切成 `<commandline>\t<pid>`，复用 parseScannerOutput
	var rebuilt []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Node,") {
			continue
		}
		commaIdx := strings.LastIndex(line, ",")
		if commaIdx < 0 {
			continue
		}
		pid := strings.TrimSpace(line[commaIdx+1:])
		cmdline := line[:commaIdx]
		rebuilt = append(rebuilt, pid+"\t"+cmdline)
	}
	return parseScannerOutput(strings.Join(rebuilt, "\n"), "\t", configPath, absPath, self)
}

// parseScannerOutput 解析 "<pid><sep><commandline>" 多行串，按相同的"命令行像不像 daemon"
// 规则过滤。sep 由 PowerShell 走 "\t"，wmic 也被预处理成 "\t"，所以这里统一。
func parseScannerOutput(raw, sep string, configPath, absPath string, self int) []int {
	var pids []int
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, sep, 2)
		if len(parts) != 2 {
			continue
		}
		pid, perr := strconv.Atoi(strings.TrimSpace(parts[0]))
		if perr != nil || pid == self || pid <= 4 {
			continue
		}
		cmdline := parts[1]
		if !strings.Contains(cmdline, "omlctl") || !strings.Contains(cmdline, "daemon start") {
			continue
		}
		if !strings.Contains(cmdline, absPath) && !strings.Contains(cmdline, configPath) {
			continue
		}
		pids = append(pids, pid)
	}
	return pids
}
