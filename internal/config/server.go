package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type ServerConfig struct {
	ListenAddr          string    `yaml:"listen_addr"`           // 控制平面 HTTP API 监听地址
	ChiselListenAddr    string    `yaml:"chisel_listen_addr"`    // chisel server 监听地址（设备隧道入口）
	ChiselAdvertiseAddr string    `yaml:"chisel_advertise_addr"` // 通告给客户端的 chisel 地址，例如 "vps.example.com:8443"；空则回落到 listen
	ChiselKeySeed       string    `yaml:"chisel_key_seed"`       // chisel server SSH 私钥种子，保持 fingerprint 稳定
	DataDir             string    `yaml:"data_dir"`
	PortPool            PortPool  `yaml:"port_pool"`
	Log                 LogConfig `yaml:"log"`
	// SSH 跳板：客户端 enroll 后用 `ssh -L` 通过这里跳进 VPS 内 chisel R-listener。
	// SSHHost 空时回退 ChiselAdvertiseAddr 的 host。SSHPort 默认 22。
	SSHHost string `yaml:"ssh_host"`
	SSHPort int    `yaml:"ssh_port"`
	// DisableSSHAcct: 关掉 VPS 账号自动管理（不调 useradd/usermod）。
	// 用途：本地开发 / e2e 跑 server 时没有 sudo 权限；CI 容器里也常这样。
	// 生产**必须**保持 false（默认）。
	DisableSSHAcct bool `yaml:"disable_ssh_acct"`
}

type PortPool struct {
	Min int `yaml:"min"`
	Max int `yaml:"max"`
}

func defaultServerConfig() ServerConfig {
	return ServerConfig{
		ListenAddr:       ":8080",
		ChiselListenAddr: ":8443",
		DataDir:          "./data",
		PortPool:         PortPool{Min: 40000, Max: 49999},
		Log:              LogConfig{Level: "info", Format: "text"},
	}
}

// LoadServer 从 YAML 文件加载配置；path 为空时返回默认配置。
func LoadServer(path string) (ServerConfig, error) {
	cfg := defaultServerConfig()
	if path == "" {
		return cfg, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("读取配置文件 %s: %w", path, err)
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("解析 YAML 失败: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (c ServerConfig) validate() error {
	if c.ListenAddr == "" {
		return fmt.Errorf("listen_addr 不能为空")
	}
	if c.ChiselListenAddr == "" {
		return fmt.Errorf("chisel_listen_addr 不能为空")
	}
	if c.DataDir == "" {
		return fmt.Errorf("data_dir 不能为空")
	}
	if c.PortPool.Min <= 0 || c.PortPool.Max <= 0 || c.PortPool.Min > c.PortPool.Max {
		return fmt.Errorf("port_pool 非法: min=%d max=%d", c.PortPool.Min, c.PortPool.Max)
	}
	if c.PortPool.Min > 65535 || c.PortPool.Max > 65535 {
		return fmt.Errorf("port_pool 超过 65535: min=%d max=%d", c.PortPool.Min, c.PortPool.Max)
	}
	return nil
}
