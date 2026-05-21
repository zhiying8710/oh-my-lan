package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type ClientConfig struct {
	ServerURL             string    `yaml:"server_url"`
	DeviceName            string    `yaml:"device_name"`
	DataDir               string    `yaml:"data_dir"`
	IPCSocket             string    `yaml:"ipc_socket"`
	ReloadIntervalSeconds int       `yaml:"reload_interval_seconds"` // daemon 轮询 bootstrap 间隔；0 用默认 30s
	Log                   LogConfig `yaml:"log"`
}

func defaultClientConfig() ClientConfig {
	return ClientConfig{
		DataDir: "./data",
		Log:     LogConfig{Level: "info", Format: "text"},
	}
}

// LoadClient 从 YAML 文件加载配置；path 为空时返回默认配置。
func LoadClient(path string) (ClientConfig, error) {
	cfg := defaultClientConfig()
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
	return cfg, nil
}
