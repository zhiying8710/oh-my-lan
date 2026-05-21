package config

import (
	"path/filepath"
	"testing"
)

func TestLoadServer_DefaultsWhenPathEmpty(t *testing.T) {
	cfg, err := LoadServer("")
	if err != nil {
		t.Fatalf("LoadServer 空路径返回错误: %v", err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr 默认值不对: %q", cfg.ListenAddr)
	}
	if cfg.PortPool.Min != 40000 || cfg.PortPool.Max != 49999 {
		t.Errorf("PortPool 默认值不对: %+v", cfg.PortPool)
	}
}

func TestLoadServer_FromExample(t *testing.T) {
	path := filepath.Join("..", "..", "configs", "server.example.yaml")
	cfg, err := LoadServer(path)
	if err != nil {
		t.Fatalf("加载示例配置失败: %v", err)
	}
	if cfg.ListenAddr == "" {
		t.Errorf("示例配置 listen_addr 应非空")
	}
}

func TestServerConfig_Validate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     ServerConfig
		wantErr bool
	}{
		{"默认合法", defaultServerConfig(), false},
		{"listen 为空", ServerConfig{PortPool: PortPool{Min: 1, Max: 2}}, true},
		{"port pool 顺序反", ServerConfig{ListenAddr: ":1", PortPool: PortPool{Min: 100, Max: 10}}, true},
		{"port pool 超 65535", ServerConfig{ListenAddr: ":1", PortPool: PortPool{Min: 100, Max: 70000}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("validate err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}
