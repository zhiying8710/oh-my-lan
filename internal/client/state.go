// Package client 实装 oh-my-lan 客户端 daemon 与 CLI 共用的逻辑：
//   - 本地 state 持久化（device_id / tunnel_secret 等）
//   - 调用服务端控制平面的 HTTP API
//   - 维持 chisel 隧道长连
package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// State 是客户端持久化在本地的全部"身份与对端信息"。
// 落盘格式 JSON，仅当前用户可读（0o600）。
type State struct {
	ServerURL         string `json:"server_url"`
	DeviceID          string `json:"device_id"`
	DeviceName        string `json:"device_name"`
	TunnelSecret      string `json:"tunnel_secret"`
	ServerFingerprint string `json:"server_fingerprint"`
	ChiselAddr        string `json:"chisel_addr"`
}

// ErrStateMissing 表示尚未 enroll 过。
var ErrStateMissing = errors.New("尚未注册：请先运行 omlctl enroll")

// StatePath 在给定 data_dir 下返回 state 文件路径。
func StatePath(dataDir string) string {
	return filepath.Join(dataDir, "state.json")
}

func LoadState(path string) (*State, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrStateMissing
		}
		return nil, fmt.Errorf("读 state 文件 %s: %w", path, err)
	}
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("解析 state: %w", err)
	}
	if s.DeviceID == "" || s.TunnelSecret == "" {
		return nil, fmt.Errorf("state 残缺：device_id 或 tunnel_secret 为空")
	}
	return &s, nil
}

func SaveState(path string, s *State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("创建 state 目录: %w", err)
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("写临时 state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("替换 state: %w", err)
	}
	return nil
}
