// Package sshacct 管理 VPS 上为每台 oml device 创建的受限 SSH 账号。
//
// 设计见 docs/security-via-ssh-tunnel.md。要点：
//   - 每台 device enroll 时调 Provision：useradd oml-<id8> + 写 authorized_keys
//     行（restrict + permitopen 限定到该 device 的 service public_port）
//   - revoke 时调 Lock：usermod -L + 清 authorized_keys + pkill -u 强断现存 session
//   - cron 7 天后调 Delete：userdel -r 真删 home 目录
//   - service 增删时调 UpdateAllowedPorts：重写 authorized_keys 的 permitopen 列表
//
// 所有 shell 操作走 sudo + /etc/sudoers.d/oml-server 显式白名单，避免 root 进程
// 直接 useradd 失去审计。详见同目录 README 和顶级 docs/security-via-ssh-tunnel.md。
package sshacct

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Manager 持有 VPS 端账号管理所需的全部配置。
//
// HomeBase 一般是 "/home"；测试时可指向 t.TempDir() 隔离。
// UseSudo=false 让测试可以在 docker/CI 沙盒里跑（不走 sudo）。
type Manager struct {
	Logger   *slog.Logger
	Prefix   string // "oml-" —— 所有受限账号必须带此前缀，与 sudoers 白名单匹配
	HomeBase string // "/home"
	UseSudo  bool   // 生产 true；测试 false（直接 exec useradd 等）
	// MockExec 让单测可以拦截 exec 调用；nil 时走真实 exec.Command。
	MockExec func(name string, args ...string) ([]byte, error)
}

// New 用默认 prod 配置构造 Manager。
func New(logger *slog.Logger) *Manager {
	return &Manager{
		Logger:   logger,
		Prefix:   "oml-",
		HomeBase: "/home",
		UseSudo:  true,
	}
}

// UsernameFor 把 device ID 转成 ssh 账号名。强制全小写 + 取前 8 位 hex。
// device ID 是 32 hex 字符（auth.NewRandomID），8 hex = 32 bit，2^32 个 device 才有
// 碰撞概率，远超 oml 的设计规模。
//
// 历史教训：如果用 device name 当账号名，中文/空格 → useradd 拒绝；前 8 hex 永远合法。
func (m *Manager) UsernameFor(deviceID string) string {
	if len(deviceID) > 8 {
		deviceID = deviceID[:8]
	}
	return m.Prefix + strings.ToLower(deviceID)
}

// validNameRE 校验账号名形态：oml- + 8 字符 hex。sudoers 也按这个 pattern 写。
var validNameRE = regexp.MustCompile(`^oml-[a-f0-9]{8}$`)

// ValidatePubkey 检查 OpenSSH 公钥格式：必须 ed25519、单行、无换行/空格畸形。
// 历史教训：rsa <2048 / dsa 早已不安全，oml 强制 ed25519 一种类型，简化攻击面。
func ValidatePubkey(line string) error {
	line = strings.TrimSpace(line)
	if line == "" {
		return errors.New("公钥为空")
	}
	if strings.ContainsAny(line, "\n\r") {
		return errors.New("公钥必须单行")
	}
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return errors.New("公钥格式应为 'ssh-ed25519 <base64> [comment]'")
	}
	if parts[0] != "ssh-ed25519" {
		return fmt.Errorf("公钥类型必须是 ssh-ed25519，得到 %q", parts[0])
	}
	if len(parts[1]) < 40 || len(parts[1]) > 256 {
		return errors.New("公钥 base64 部分长度异常")
	}
	return nil
}

// Provision 在 VPS 上为 device 创建账号 + 写 authorized_keys。idempotent：
// 重复调用同一 deviceID 不报错，但会覆盖之前的 authorized_keys（用最新 pubkey）。
//
// allowedPorts 是该 device 的所有 service public_port，逐个写成 permitopen=127.0.0.1:port。
// 为空时仍创建账号 + 写 authorized_keys（restrict 行，nopermitopen），意思是 ssh 能登
// 但不能 forward 任何端口——直到该 device 发布第一个 service 后 UpdateAllowedPorts 才解锁。
func (m *Manager) Provision(ctx context.Context, deviceID, pubkey string, allowedPorts []int) (string, error) {
	if err := ValidatePubkey(pubkey); err != nil {
		return "", fmt.Errorf("公钥校验失败: %w", err)
	}
	user := m.UsernameFor(deviceID)
	if !validNameRE.MatchString(user) {
		return "", fmt.Errorf("生成的账号名不合法: %q", user)
	}

	// 1. useradd 幂等：先看账号是否已存在；存在 → 直接覆盖 authorized_keys 即可。
	exists, err := m.userExists(ctx, user)
	if err != nil {
		return "", fmt.Errorf("检查账号是否存在: %w", err)
	}
	if !exists {
		out, err := m.run(ctx, "useradd", "-m", "-s", "/usr/sbin/nologin", user)
		if err != nil {
			return "", fmt.Errorf("useradd %s 失败: %w (output: %s)", user, err, string(out))
		}
	}

	// 2. 准备 .ssh 目录 + authorized_keys
	if err := m.writeAuthorizedKeys(ctx, user, pubkey, allowedPorts); err != nil {
		// rollback：刚 useradd 出来的账号要清掉，避免半状态
		if !exists {
			_, _ = m.run(ctx, "userdel", "-r", user)
		}
		return "", err
	}

	m.Logger.Info("ssh 账号已就绪", "device", deviceID, "user", user, "allowed_ports", allowedPorts)
	return user, nil
}

// Lock 立即锁定账号 + 清 authorized_keys + 强杀 session。撤销 device 的第一步。
// 真删 home 目录留给 cron（grace period 后调 Delete）。idempotent：账号不存在不报错。
func (m *Manager) Lock(ctx context.Context, deviceID string) error {
	user := m.UsernameFor(deviceID)
	if !validNameRE.MatchString(user) {
		return fmt.Errorf("账号名不合法: %q", user)
	}
	exists, err := m.userExists(ctx, user)
	if err != nil {
		return fmt.Errorf("检查账号是否存在: %w", err)
	}
	if !exists {
		m.Logger.Info("ssh 账号不存在，跳过 lock", "device", deviceID, "user", user)
		return nil
	}
	if _, err := m.run(ctx, "usermod", "-L", user); err != nil {
		m.Logger.Warn("usermod -L 失败", "user", user, "err", err)
		// 继续尝试清 authorized_keys + pkill，best-effort
	}
	// 清 authorized_keys：即便密码登录被禁，清掉确保 SSH key 也用不了
	authPath := filepath.Join(m.HomeBase, user, ".ssh", "authorized_keys")
	if err := os.WriteFile(authPath, []byte(""), 0600); err != nil && !os.IsNotExist(err) {
		m.Logger.Warn("清 authorized_keys 失败", "user", user, "err", err)
	}
	// 强断现存 session
	if _, err := m.run(ctx, "pkill", "-u", user); err != nil {
		// pkill 没找到进程会返回 exit 1，不算错
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			m.Logger.Warn("pkill -u 失败", "user", user, "err", err)
		}
	}
	m.Logger.Info("ssh 账号已锁定", "device", deviceID, "user", user)
	return nil
}

// Delete 真删账号（userdel -r 含 home 目录）。cron 在 grace period 后调。
// idempotent：账号不存在不报错。
func (m *Manager) Delete(ctx context.Context, deviceID string) error {
	user := m.UsernameFor(deviceID)
	if !validNameRE.MatchString(user) {
		return fmt.Errorf("账号名不合法: %q", user)
	}
	exists, err := m.userExists(ctx, user)
	if err != nil {
		return fmt.Errorf("检查账号是否存在: %w", err)
	}
	if !exists {
		return nil
	}
	out, err := m.run(ctx, "userdel", "-r", user)
	if err != nil {
		return fmt.Errorf("userdel %s: %w (output: %s)", user, err, string(out))
	}
	m.Logger.Info("ssh 账号已删除", "device", deviceID, "user", user)
	return nil
}

// UpdateAllowedPorts 重写 authorized_keys 的 permitopen 列表（service 增删后调）。
// 需要原始 pubkey——调用方从 store 拿。idempotent。
func (m *Manager) UpdateAllowedPorts(ctx context.Context, deviceID, pubkey string, allowedPorts []int) error {
	user := m.UsernameFor(deviceID)
	if !validNameRE.MatchString(user) {
		return fmt.Errorf("账号名不合法: %q", user)
	}
	exists, err := m.userExists(ctx, user)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("账号 %s 不存在", user)
	}
	return m.writeAuthorizedKeys(ctx, user, pubkey, allowedPorts)
}

// authorizedKeysLine 构造 authorized_keys 单行：
//
//	restrict,port-forwarding,permitopen="127.0.0.1:40000",permitopen="127.0.0.1:40001" ssh-ed25519 AAAA... oml-managed
//
// restrict = no-X11-forwarding + no-agent-forwarding + no-port-forwarding + no-pty + no-user-rc + no-touch-required (OpenSSH 8.5+)
// 然后单独再开 port-forwarding（restrict 关闭了它），并 permitopen 限制只能 forward 到指定端口。
// 没 permitopen 时整行就是 "restrict,port-forwarding" → 能登但 forward 任何端口都被拒。
func authorizedKeysLine(pubkey string, allowedPorts []int) string {
	pubkey = strings.TrimSpace(pubkey)
	if !strings.HasSuffix(pubkey, "oml-managed") {
		// 加 comment 让运维 cat authorized_keys 一眼能看出是 oml 写的
		// 即便用户自己也手动改过 authorized_keys，oml-managed 的行也能 grep 出来。
		pubkey = pubkey + " oml-managed"
	}
	parts := []string{"restrict", "port-forwarding"}
	for _, p := range allowedPorts {
		parts = append(parts, fmt.Sprintf(`permitopen="127.0.0.1:%d"`, p))
	}
	return strings.Join(parts, ",") + " " + pubkey
}

// writeAuthorizedKeys 写入 /home/<user>/.ssh/authorized_keys。需要 root 权限做 chown。
func (m *Manager) writeAuthorizedKeys(ctx context.Context, user, pubkey string, allowedPorts []int) error {
	home := filepath.Join(m.HomeBase, user)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return fmt.Errorf("mkdir .ssh: %w", err)
	}
	line := authorizedKeysLine(pubkey, allowedPorts) + "\n"
	authPath := filepath.Join(sshDir, "authorized_keys")
	if err := os.WriteFile(authPath, []byte(line), 0600); err != nil {
		return fmt.Errorf("写 authorized_keys: %w", err)
	}
	// chown 给账号自己；root 写完归属归属还是 root，sshd 会拒绝
	if _, err := m.run(ctx, "chown", "-R", user+":"+user, sshDir); err != nil {
		return fmt.Errorf("chown .ssh: %w", err)
	}
	return nil
}

// userExists 通过 /etc/passwd 查账号。getent 走 nss 更全；这里用 /etc/passwd 因为
// 我们自己 useradd 的账号一定在本地文件，简化依赖。
func (m *Manager) userExists(ctx context.Context, user string) (bool, error) {
	f, err := os.Open("/etc/passwd")
	if err != nil {
		// 测试场景文件不存在视为"没账号"
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()
	prefix := user + ":"
	buf := make([]byte, 64*1024)
	n, _ := f.Read(buf)
	for _, line := range strings.Split(string(buf[:n]), "\n") {
		if strings.HasPrefix(line, prefix) {
			return true, nil
		}
	}
	return false, nil
}

// run 执行外部命令；UseSudo 时套 sudo -n（non-interactive，密码必须配 NOPASSWD）。
// MockExec 优先，让单测注入。
func (m *Manager) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if m.MockExec != nil {
		return m.MockExec(name, args...)
	}
	cmdArgs := append([]string{}, args...)
	binName := name
	if m.UseSudo {
		// 走 /usr/sbin/<name> 完整路径，sudoers 用 Cmnd_Alias 配的也是完整路径
		fullPath := absoluteCommandPath(name)
		cmdArgs = append([]string{"-n", fullPath}, args...)
		binName = "sudo"
	}
	cmd := exec.CommandContext(ctx, binName, cmdArgs...)
	return cmd.CombinedOutput()
}

// absoluteCommandPath 把 useradd / usermod / userdel / pkill / chown 解析到绝对路径，
// 与 sudoers 白名单的命令行匹配。
func absoluteCommandPath(name string) string {
	candidates := map[string][]string{
		"useradd": {"/usr/sbin/useradd"},
		"usermod": {"/usr/sbin/usermod"},
		"userdel": {"/usr/sbin/userdel"},
		"pkill":   {"/usr/bin/pkill"},
		"chown":   {"/usr/bin/chown", "/bin/chown"},
	}
	for _, p := range candidates[name] {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	// 默认按 PATH 解析（sudoers 不会 match，但起码能跑测试）
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return name
}

// 辅助：把 int 切片转字符串供日志输出
func portsString(ps []int) string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = strconv.Itoa(p)
	}
	return strings.Join(out, ",")
}

var _ = portsString // 留给将来 log 用，避免 unused
