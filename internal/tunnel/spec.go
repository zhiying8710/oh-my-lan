// Package tunnel 把 chisel server / client 封装成 oh-my-lan 用得顺手的形态。
package tunnel

import (
	"fmt"
	"strings"
)

// Remote 描述一条 reverse forward 规则：服务端公网端口 → 客户端本地地址。
type Remote struct {
	PublicPort int    // VPS 上对外监听的端口（bind 地址由 BindLocal 决定）
	LocalAddr  string // 客户端本机目标地址，例如 "127.0.0.1:22"
	Protocol   string // "tcp" 或 "udp"
	// BindLocal 控制 chisel R-listener 在 VPS 上 bind 0.0.0.0 还是 127.0.0.1。
	// true（默认安全）→ 127.0.0.1，公网扫不到，要 ssh -L 跳板才能访问。
	// false → 0.0.0.0，全互联网可达（历史教训：mini-pc 的 RDP 这么开就被勒索了）。
	BindLocal bool
}

// Local 描述一条 local forward 规则：客户端本机端口 → server 上的目标端口（通过 chisel session 转发）。
// 用于 mesh 场景：A 客户端 forward 到 server，server 再 reverse forward 给 B 客户端。
type Local struct {
	LocalPort        int    // A 本机监听端口（仅 127.0.0.1）
	RemotePublicPort int    // chisel server 上的目标端口（B 的 service public_port）
	Protocol         string // "tcp" 或 "udp"
}

func (l Local) Validate() error {
	if l.LocalPort <= 0 || l.LocalPort > 65535 {
		return fmt.Errorf("local_port 非法: %d", l.LocalPort)
	}
	if l.RemotePublicPort <= 0 || l.RemotePublicPort > 65535 {
		return fmt.Errorf("remote_public_port 非法: %d", l.RemotePublicPort)
	}
	switch strings.ToLower(l.Protocol) {
	case "tcp", "udp":
	default:
		return fmt.Errorf("protocol 必须是 tcp/udp，实际: %q", l.Protocol)
	}
	return nil
}

// ToChiselSpec 把 Local 编码成 chisel client 的 forward spec。
// chisel 没有 "L:" 前缀，forward 是默认；只有 "R:" 表示 reverse。
// 例：127.0.0.1:8022:127.0.0.1:41001 表示在本机 8022 监听，
// 经 chisel session 拨号 server 上的 127.0.0.1:41001（B 的 reverse listener）。
func (l Local) ToChiselSpec() string {
	spec := fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", l.LocalPort, l.RemotePublicPort)
	if strings.EqualFold(l.Protocol, "udp") {
		spec += "/udp"
	}
	return spec
}

// Validate 做最小校验。
func (r Remote) Validate() error {
	if r.PublicPort <= 0 || r.PublicPort > 65535 {
		return fmt.Errorf("public_port 非法: %d", r.PublicPort)
	}
	if r.LocalAddr == "" {
		return fmt.Errorf("local_addr 不能为空")
	}
	switch strings.ToLower(r.Protocol) {
	case "tcp", "udp":
	default:
		return fmt.Errorf("protocol 必须是 tcp/udp，实际: %q", r.Protocol)
	}
	return nil
}

// ToChiselSpec 把 Remote 编码成 chisel client 的 R: spec 字符串。
// 例如 R:127.0.0.1:40001:127.0.0.1:22（仅本机）或 R:0.0.0.0:40001:127.0.0.1:22（公网，高危）。
//
// bind 地址由 server 在 BootstrapResponse.Remotes[i].BindLocal 控制——daemon 只是
// 透传 server 的决定。server 端写库时强制 bind_local=true 即可全局封堵公网。
func (r Remote) ToChiselSpec() string {
	bind := "0.0.0.0"
	if r.BindLocal {
		bind = "127.0.0.1"
	}
	spec := fmt.Sprintf("R:%s:%d:%s", bind, r.PublicPort, r.LocalAddr)
	if strings.EqualFold(r.Protocol, "udp") {
		spec += "/udp"
	}
	return spec
}
