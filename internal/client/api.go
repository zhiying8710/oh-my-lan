package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zhiying8710/oh-my-lan/internal/proto"
)

// ErrUnauthorized 表示服务端返回 401。
// daemon 调用 bootstrap 拿到这个错误时，意味着自身已被 admin 撤销，
// 应该立刻终止而不是无限重试。
var ErrUnauthorized = errors.New("服务端返回 401：device 凭证已失效或被撤销")

// APIClient 是控制平面的 HTTP 客户端。
//
// BaseURL 是控制平面地址（不带尾斜杠），DeviceID/Secret 用于 Bearer 认证。
// Enroll 流程在没有 DeviceID 时调用，认证字段允许为空。
type APIClient struct {
	BaseURL    string
	DeviceID   string
	Secret     string
	HTTP       *http.Client
}

func NewAPIClient(serverURL string) *APIClient {
	return &APIClient{
		BaseURL: strings.TrimRight(serverURL, "/"),
		HTTP:    &http.Client{Timeout: 15 * time.Second},
	}
}

// Enroll 用一次性 token 把当前设备注册到服务端。
// sshPubkey 是客户端事先 EnsureSSHKey 拿到的 OpenSSH 单行公钥；server 用它建 VPS 受限账号。
func (c *APIClient) Enroll(ctx context.Context, token, deviceName, sshPubkey string) (proto.EnrollDeviceResponse, error) {
	var out proto.EnrollDeviceResponse
	err := c.do(ctx, http.MethodPost, "/api/devices/enroll", false,
		proto.EnrollDeviceRequest{Token: token, DeviceName: deviceName, SSHPubkey: sshPubkey}, &out)
	return out, err
}

func (c *APIClient) AddService(ctx context.Context, req proto.AddServiceRequest) (proto.ServiceDTO, error) {
	var out proto.ServiceDTO
	err := c.do(ctx, http.MethodPost, "/api/services", true, req, &out)
	return out, err
}

func (c *APIClient) ListServices(ctx context.Context) ([]proto.ServiceDTO, error) {
	var out proto.ListServicesResponse
	if err := c.do(ctx, http.MethodGet, "/api/services", true, nil, &out); err != nil {
		return nil, err
	}
	return out.Services, nil
}

func (c *APIClient) DeleteService(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/services/"+id, true, nil, nil)
}

func (c *APIClient) EnableService(ctx context.Context, id string) (proto.ServiceDTO, error) {
	var out proto.ServiceDTO
	err := c.do(ctx, http.MethodPost, "/api/services/"+id+"/enable", true, nil, &out)
	return out, err
}

func (c *APIClient) DisableService(ctx context.Context, id string) (proto.ServiceDTO, error) {
	var out proto.ServiceDTO
	err := c.do(ctx, http.MethodPost, "/api/services/"+id+"/disable", true, nil, &out)
	return out, err
}

func (c *APIClient) Bootstrap(ctx context.Context) (proto.BootstrapResponse, error) {
	var out proto.BootstrapResponse
	err := c.do(ctx, http.MethodGet, "/api/devices/me/bootstrap", true, nil, &out)
	return out, err
}

func (c *APIClient) DiscoverServices(ctx context.Context) ([]proto.ServiceBriefDTO, error) {
	var out proto.DiscoverDTO
	if err := c.do(ctx, http.MethodGet, "/api/devices/me/discover", true, nil, &out); err != nil {
		return nil, err
	}
	return out.Services, nil
}

func (c *APIClient) ListAllServices(ctx context.Context) ([]proto.ServiceBriefDTO, error) {
	var out proto.ListAllServicesResponse
	if err := c.do(ctx, http.MethodGet, "/api/services/all", true, nil, &out); err != nil {
		return nil, err
	}
	return out.Services, nil
}

func (c *APIClient) AddForward(ctx context.Context, req proto.AddForwardRequest) (proto.ForwardDTO, error) {
	var out proto.ForwardDTO
	err := c.do(ctx, http.MethodPost, "/api/forwards", true, req, &out)
	return out, err
}

func (c *APIClient) ListForwards(ctx context.Context) ([]proto.ForwardDTO, error) {
	var out proto.ListForwardsResponse
	if err := c.do(ctx, http.MethodGet, "/api/forwards", true, nil, &out); err != nil {
		return nil, err
	}
	return out.Forwards, nil
}

func (c *APIClient) DeleteForward(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/forwards/"+id, true, nil, nil)
}

func (c *APIClient) EnableForward(ctx context.Context, id string) (proto.ForwardDTO, error) {
	var out proto.ForwardDTO
	err := c.do(ctx, http.MethodPost, "/api/forwards/"+id+"/enable", true, nil, &out)
	return out, err
}

func (c *APIClient) DisableForward(ctx context.Context, id string) (proto.ForwardDTO, error) {
	var out proto.ForwardDTO
	err := c.do(ctx, http.MethodPost, "/api/forwards/"+id+"/disable", true, nil, &out)
	return out, err
}

func (c *APIClient) do(ctx context.Context, method, path string, needAuth bool, in any, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if needAuth {
		if c.DeviceID == "" || c.Secret == "" {
			return fmt.Errorf("调用 %s 需要认证，但未配置 device_id / secret", path)
		}
		req.Header.Set("Authorization", "Bearer "+c.DeviceID+"."+c.Secret)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("请求 %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusUnauthorized {
		// 让上层（daemon）能 errors.Is 检测，触发 device-revoked 退出路径
		return fmt.Errorf("%w (%s)", ErrUnauthorized, strings.TrimSpace(string(raw)))
	}
	if resp.StatusCode >= 400 {
		var e proto.ErrorResponse
		_ = json.Unmarshal(raw, &e)
		if e.Error != "" {
			return fmt.Errorf("服务端返回 %d: %s", resp.StatusCode, e.Error)
		}
		return fmt.Errorf("服务端返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("解析响应: %w", err)
		}
	}
	return nil
}
