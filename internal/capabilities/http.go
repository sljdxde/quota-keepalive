package capabilities

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// UserAgent 是 QKA 的稳定技术标识，沿用兼容值 qka/1.0。
	UserAgent = "qka/1.0"
	// DefaultHTTPTimeout 是出站请求默认超时。
	DefaultHTTPTimeout = 15 * time.Second
	// MaxResponseBodyBytes 限制响应体，防止适配器拉取超大响应。
	MaxResponseBodyBytes = 1 << 20
)

// ErrHostNotAllowed 表示目标主机不在白名单内。
var ErrHostNotAllowed = errors.New("目标主机不在白名单，拒绝出站请求")

// AllowedHosts 是目标范围白名单（按 host:port 匹配，port 可省略）。
type AllowedHosts map[string]bool

// AllowedHostsFromManifest 用 manifest 声明的主机列表构造白名单。
func AllowedHostsFromManifest(hosts []string) AllowedHosts {
	a := AllowedHosts{}
	for _, h := range hosts {
		a[strings.ToLower(h)] = true
	}
	return a
}

func (a AllowedHosts) check(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	host := strings.ToLower(u.Host)
	if a[host] {
		return nil
	}
	// 允许白名单仅写主机名（不限端口）的情况。
	if h := strings.ToLower(u.Hostname()); a[h] {
		return nil
	}
	return ErrHostNotAllowed
}

// FetchRequest 是出站请求描述。
type FetchRequest struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    []byte
}

// FetchResponse 是出站响应（正文已限大小）。
type FetchResponse struct {
	Status  int
	Headers map[string]string
	Body    []byte
}

// HTTPFetch 是统一出站 HTTP 能力。
type HTTPFetch struct {
	client  *http.Client
	allowed AllowedHosts
	grant   Grant
}

// NewHTTPFetch 组装出站 HTTP 能力。
func NewHTTPFetch(allowed AllowedHosts, grant Grant) *HTTPFetch {
	return &HTTPFetch{
		client:  &http.Client{Timeout: DefaultHTTPTimeout},
		allowed: allowed,
		grant:   grant,
	}
}

// WithTimeout 覆盖请求超时（主要用于测试）。
func (h *HTTPFetch) WithTimeout(d time.Duration) *HTTPFetch {
	h.client.Timeout = d
	return h
}

// Fetch 执行一次出站请求：授权门禁、白名单、统一 UA、超时与响应大小限制。
func (h *HTTPFetch) Fetch(ctx context.Context, in FetchRequest) (FetchResponse, error) {
	if err := h.grant.Authorize("http.outbound"); err != nil {
		return FetchResponse{}, err
	}
	if err := h.allowed.check(in.URL); err != nil {
		return FetchResponse{}, err
	}

	method := in.Method
	if method == "" {
		method = http.MethodGet
	}

	var bodyReader io.Reader
	if in.Body != nil {
		bodyReader = bytes.NewReader(in.Body)
	}

	req, err := http.NewRequestWithContext(ctx, method, in.URL, bodyReader)
	if err != nil {
		return FetchResponse{}, err
	}
	for k, v := range in.Headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", UserAgent)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return FetchResponse{}, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBodyBytes))
	if err != nil {
		return FetchResponse{}, err
	}

	headers := map[string]string{}
	for k, v := range resp.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}
	return FetchResponse{Status: resp.StatusCode, Headers: headers, Body: data}, nil
}

var sensitiveKeys = []string{"token", "key", "secret", "sign", "auth", "password", "credential"}

// RedactURL 返回适合写日志的 URL：敏感 query 参数值替换为 REDACTED。
func RedactURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "REDACTED"
	}
	q := u.Query()
	for k := range q {
		lower := strings.ToLower(k)
		for _, s := range sensitiveKeys {
			if strings.Contains(lower, s) {
				q.Set(k, "REDACTED")
				break
			}
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}
