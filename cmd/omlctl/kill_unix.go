//go:build unix

package main

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// isProcessAlive 在 Unix 上用 `kill(pid, 0)` 探测；nil → 存活，ESRCH → 不存在，
// EPERM → 存在但无权限（仍视作存活）。
func isProcessAlive(pid int) bool {
	err := syscall.Kill(pid, syscall.Signal(0))
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}

// sendTerm 发 SIGTERM（请求子进程优雅退出）。
func sendTerm(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

// sendKill 发 SIGKILL（强制终结）。
func sendKill(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) }

// processLooksLikeOmlctl 用 ps 验证 pid 对应进程的命令行里含 "omlctl"，
// 防止 pid 复用时把无关进程误杀。
func processLooksLikeOmlctl(pid int) bool {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), "omlctl")
}

// psScanOmlctlByConfig 通过 `ps -eo pid,command` 扫所有进程，
// 命令行同时包含 `omlctl` + `daemon start` + 给定 config 路径的 pid 返回。
// 自身 pid 排除在外。失败回退到空 slice（保守）。
func psScanOmlctlByConfig(configPath, absPath string, self int) []int {
	out, err := exec.Command("ps", "-eo", "pid,command").Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid == self || pid <= 1 {
			continue
		}
		cmd := strings.Join(fields[1:], " ")
		if !strings.Contains(cmd, "omlctl") || !strings.Contains(cmd, "daemon start") {
			continue
		}
		if !strings.Contains(cmd, absPath) && !strings.Contains(cmd, configPath) {
			continue
		}
		pids = append(pids, pid)
	}
	return pids
}
