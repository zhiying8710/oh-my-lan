package tunnel

import (
	"context"
	"fmt"
	"time"

	chclient "github.com/jpillora/chisel/client"
)

// ClientConfig 是 Client 的构建参数。
type ClientConfig struct {
	ServerURL   string   // 例如 "http://vps:8443"
	Fingerprint string   // chisel server 的 SSH fingerprint；为空则不校验
	DeviceID    string   // chisel auth user
	Secret      string   // chisel auth password (tunnel secret)
	Remotes     []Remote // 本设备发布的服务（chisel R:）
	Locals      []Local  // 本设备消费的远端服务 forward（chisel L:）
	KeepAlive   time.Duration
	Verbose     bool
}

// Client 包装 chisel client。
type Client struct {
	cli *chclient.Client
}

func NewClient(cfg ClientConfig) (*Client, error) {
	if cfg.ServerURL == "" {
		return nil, fmt.Errorf("server_url 不能为空")
	}
	if cfg.DeviceID == "" || cfg.Secret == "" {
		return nil, fmt.Errorf("device_id / secret 不能为空")
	}

	specs := make([]string, 0, len(cfg.Remotes)+len(cfg.Locals))
	for _, r := range cfg.Remotes {
		if err := r.Validate(); err != nil {
			return nil, err
		}
		specs = append(specs, r.ToChiselSpec())
	}
	for _, l := range cfg.Locals {
		if err := l.Validate(); err != nil {
			return nil, err
		}
		specs = append(specs, l.ToChiselSpec())
	}

	keepAlive := cfg.KeepAlive
	if keepAlive == 0 {
		keepAlive = 25 * time.Second
	}

	cli, err := chclient.NewClient(&chclient.Config{
		Server:           cfg.ServerURL,
		Fingerprint:      cfg.Fingerprint,
		Auth:             cfg.DeviceID + ":" + cfg.Secret,
		Remotes:          specs,
		KeepAlive:        keepAlive,
		MaxRetryCount:    -1,              // -1 = 永久重试，由我们的 daemon 层 ctx 控制退出
		MaxRetryInterval: 30 * time.Second, // 默认 5min 对个人用太长
		Verbose:          cfg.Verbose,
	})
	if err != nil {
		return nil, fmt.Errorf("构建 chisel client: %w", err)
	}
	return &Client{cli: cli}, nil
}

// Run 启动并阻塞，直到 ctx 取消或连接错误。
func (c *Client) Run(ctx context.Context) error {
	if err := c.cli.Start(ctx); err != nil {
		return fmt.Errorf("chisel client Start: %w", err)
	}
	return c.cli.Wait()
}

// Close 强制关闭。
func (c *Client) Close() error { return c.cli.Close() }
