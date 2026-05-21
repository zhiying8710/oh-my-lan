package server

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// loadOrCreateKeySeed 保证服务端在多次启动间使用相同的 chisel SSH 私钥 seed，
// 否则客户端固定的 fingerprint 在 server 重启后就失效了。
//
// 优先级：
//  1. 显式 explicit（来自配置 chisel_key_seed）：直接返回
//  2. 否则读 dataDir/chisel.seed
//  3. 不存在则生成 32 字节随机 base64，写盘 chmod 600
func loadOrCreateKeySeed(dataDir, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	path := filepath.Join(dataDir, "chisel.seed")
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		s := string(raw)
		if len(s) == 0 {
			return "", fmt.Errorf("chisel.seed 文件为空: %s", path)
		}
		return s, nil
	case errors.Is(err, os.ErrNotExist):
		// 继续生成
	default:
		return "", fmt.Errorf("读 chisel.seed: %w", err)
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成 chisel seed: %w", err)
	}
	seed := base64.RawURLEncoding.EncodeToString(buf)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", fmt.Errorf("创建 data_dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		return "", fmt.Errorf("写 chisel.seed: %w", err)
	}
	return seed, nil
}
