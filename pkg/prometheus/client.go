package prometheus

import (
	"encoding/base64"
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
)

// Client 封装 Prometheus 客户端
type Client struct {
	API v1.API
}

// basicAuthRoundTripper 实现 Basic Auth 鉴权
type basicAuthRoundTripper struct {
	username string
	password string
	next     http.RoundTripper
}

func (b *basicAuthRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// 创建 Basic Auth 头
	auth := b.username + ":" + b.password
	encodedAuth := base64.StdEncoding.EncodeToString([]byte(auth))

	// 添加 Authorization 头
	req.Header.Set("Authorization", "Basic "+encodedAuth)

	return b.next.RoundTrip(req)
}

// NewClient 创建新的 Prometheus 客户端
func NewClient(url, username, password string) (*Client, error) {
	// 定义客户端配置
	config := api.Config{
		Address: url,
	}
	if username != "" && password != "" {
		// 创建 Basic Auth 鉴权
		config = api.Config{
			Address: url,
			RoundTripper: &basicAuthRoundTripper{
				username: username,
				password: password,
				next:     http.DefaultTransport,
			},
		}
	}
	client, err := api.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("creating prometheus client: %w", err)
	}

	return &Client{
		API: v1.NewAPI(client),
	}, nil
}
