// Package bridge 把 stdio（newline-delimited JSON-RPC）转发到同一 loopback HTTP
// MCP 端点，使 stdio 与 HTTP 两种传输暴露相同契约；输出有界且不回显配对凭据。
package bridge

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"time"
)

const (
	// DefaultMaxResponseBytes 是单次响应的有界上限（1MiB）。
	DefaultMaxResponseBytes = 1 << 20
	// MaxRequestBytes 是 stdio 单行请求的有界上限。
	MaxRequestBytes = 1 << 20
)

var (
	// ErrResponseTooLarge 表示响应超过有界上限。
	ErrResponseTooLarge = errors.New("bridge: 响应超过有界上限")
)

// Config 是桥接配置。
type Config struct {
	// Endpoint 是 loopback MCP HTTP 端点（含 /mcp）。
	Endpoint string
	// HostID 与 Token 是配对凭据（仅放入请求头，不写 stdout）。
	HostID string
	Token  string
	// In 是 stdio 输入；Out 是 JSON-RPC 输出。
	In  io.Reader
	Out io.Writer
	// MaxResponseBytes 为 0 时用默认值。
	MaxResponseBytes int64
	// HTTPClient 为 nil 时用默认客户端。
	HTTPClient *http.Client
}

// Run 逐行读取 stdio 并转发到 HTTP，把响应写回 stdout。
func Run(ctx context.Context, cfg Config) error {
	max := cfg.MaxResponseBytes
	if max <= 0 {
		max = DefaultMaxResponseBytes
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	scanner := bufio.NewScanner(cfg.In)
	scanner.Buffer(make([]byte, 0, 64*1024), MaxRequestBytes)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		resp, err := forward(ctx, client, cfg.Endpoint, cfg.HostID, cfg.Token, line, max)
		if err != nil {
			return err
		}
		if _, err := cfg.Out.Write(append(resp, '\n')); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func forward(ctx context.Context, client *http.Client, endpoint, hostID, token string,
	body []byte, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// 配对凭据仅出现在请求头，不进入任何输出。
	if hostID != "" {
		req.Header.Set("X-QKA-Host", hostID)
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// 多读 1 字节以检测超限。
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, ErrResponseTooLarge
	}
	return data, nil
}
