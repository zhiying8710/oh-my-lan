package config

import (
	"path/filepath"
	"testing"
)

func TestLoadClient_DefaultsWhenPathEmpty(t *testing.T) {
	cfg, err := LoadClient("")
	if err != nil {
		t.Fatalf("LoadClient 空路径返回错误: %v", err)
	}
	if cfg.DataDir != "./data" {
		t.Errorf("DataDir 默认值不对: %q", cfg.DataDir)
	}
	if cfg.Log.Level != "info" {
		t.Errorf("Log.Level 默认值不对: %q", cfg.Log.Level)
	}
}

func TestLoadClient_FromExample(t *testing.T) {
	path := filepath.Join("..", "..", "configs", "client.example.yaml")
	cfg, err := LoadClient(path)
	if err != nil {
		t.Fatalf("加载示例配置失败: %v", err)
	}
	if cfg.ServerURL == "" {
		t.Errorf("示例配置 server_url 应非空")
	}
	if cfg.DeviceName == "" {
		t.Errorf("示例配置 device_name 应非空")
	}
}

func TestLoadClient_MissingFile(t *testing.T) {
	_, err := LoadClient("/path/that/does/not/exist.yaml")
	if err == nil {
		t.Fatal("缺失文件应返回错误")
	}
}
