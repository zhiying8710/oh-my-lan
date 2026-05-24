// internal/client/sshkey.go: 客户端 ed25519 SSH keypair 生成与持久化。
//
// 设计见 docs/security-via-ssh-tunnel.md。要点：
//   - enroll 时自动生成 ed25519 keypair 存 dataDir/ssh_key（权限 0600）。
//   - 私钥**永远不离开客户端本机**；公钥通过 enroll API 传到 server，server 写到 VPS authorized_keys。
//   - 已存在的 ssh_key 不重生成（让 re-enroll 不撕裂用户对应账号关系）。
//   - 同时落 ssh_key.pub 让用户/桌面 GUI 一眼能拿到公钥（用于排错 / 手动配 ~/.ssh/config）。
//
// 该 keypair **仅**用于"oml-* 受限 SSH 账号"——与用户自己的 ~/.ssh/id_ed25519 隔离，
// 互不污染。

package client

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// EnsureSSHKey 在 dataDir 下确保有一份 ed25519 keypair。
// 已存在 → 读出 pubkey 返回（私钥路径 + 公钥单行）。
// 不存在 → 新生成，落 ssh_key (priv) + ssh_key.pub (OpenSSH 单行格式)。
//
// 返回的 pubkeyLine 是 OpenSSH authorized_keys 格式的单行：
//
//	ssh-ed25519 AAAA... oml-managed
func EnsureSSHKey(dataDir string) (privPath, pubkeyLine string, err error) {
	if dataDir == "" {
		return "", "", fmt.Errorf("dataDir 为空")
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return "", "", fmt.Errorf("mkdir dataDir: %w", err)
	}
	privPath = filepath.Join(dataDir, "ssh_key")
	pubPath := privPath + ".pub"

	if line, err := readPubFromDisk(pubPath); err == nil && line != "" {
		// 既然 pubkey 文件存在，trust 它；返回。
		return privPath, line, nil
	}

	// 不存在 → 新生成
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("生成 ed25519 keypair: %w", err)
	}

	// 私钥写 OpenSSH 格式（PEM block "OPENSSH PRIVATE KEY"）
	pemBlock, err := ssh.MarshalPrivateKey(priv, "oml-managed")
	if err != nil {
		return "", "", fmt.Errorf("marshal 私钥: %w", err)
	}
	if err := os.WriteFile(privPath, pem.EncodeToMemory(pemBlock), 0600); err != nil {
		return "", "", fmt.Errorf("写私钥: %w", err)
	}

	// 公钥写 authorized_keys 单行格式
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", fmt.Errorf("生成 ssh.PublicKey: %w", err)
	}
	pubkeyLine = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " oml-managed"
	if err := os.WriteFile(pubPath, []byte(pubkeyLine+"\n"), 0600); err != nil {
		return "", "", fmt.Errorf("写公钥: %w", err)
	}

	return privPath, pubkeyLine, nil
}

// readPubFromDisk 读 dataDir/ssh_key.pub 单行格式公钥。
// 不存在 / 读失败返回 ""——调用方据此决定是否重新生成。
func readPubFromDisk(pubPath string) (string, error) {
	data, err := os.ReadFile(pubPath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
