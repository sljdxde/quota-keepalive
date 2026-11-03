// Package feedback implements privacy-safe bug reports for the public project.
package feedback

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// DefaultEndpoint is the Cloudflare Worker that owns the GitHub write token.
	// It can be overridden for local acceptance tests with QKA_FEEDBACK_URL.
	DefaultEndpoint = "https://qka-feedback.neighborhood-share-sljdxde.workers.dev/api/feedback"
	// DefaultSiteKey is a public anti-crawler key, not an authorization secret.
	DefaultSiteKey = "qka-feedback-2026-v1"

	maxDescriptionRunes = 12000
	maxImages           = 6
	maxImageBytes       = 1400 * 1024
	maxTotalImageBytes  = 7 * 1024 * 1024
	maxLogLines         = 80
	maxLogsBytes        = 100 * 1024
	maxResponseBytes    = 64 * 1024
)

var (
	// ErrEndpointUnavailable reports a build without a feedback proxy endpoint.
	ErrEndpointUnavailable = errors.New("feedback: 反馈服务尚未配置")
	// ErrInvalidInput reports a report that cannot be submitted safely.
	ErrInvalidInput = errors.New("feedback: 反馈内容无效")
)

// EndpointProvider 让调用方在每次提交时给出当前生效的反馈地址（例如从设置里
// 读到的自定义地址）。返回空字符串表示继续使用 Endpoint/环境变量。
type EndpointProvider func() string

// HTTPDoer is the subset of http.Client used by the service.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// LogEntry is a redacted audit log row selected by the application layer.
type LogEntry struct {
	TS        string
	Target    string
	AccountID string
	Operation string
	State     string
	Level     string
	Message   string
}

// Image is a browser-reencoded screenshot. DataURL must contain PNG, JPEG, or
// WebP bytes; SVG and arbitrary remote URLs are deliberately unsupported.
type Image struct {
	Name    string `json:"name"`
	DataURL string `json:"data_url"`
}

// Request is one user-authored report.
type Request struct {
	Description string
	Images      []Image
	IncludeLogs bool
}

// Context is the safe preview shown before submission.
type Context struct {
	Available  bool   `json:"available"`
	LogCount   int    `json:"log_count"`
	Excerpt    string `json:"excerpt,omitempty"`
	Notice     string `json:"notice"`
	MaxImages  int    `json:"max_images"`
	MaxImageMB int    `json:"max_image_mb"`
}

// Result is the created GitHub issue projection.
type Result struct {
	OK     bool   `json:"ok"`
	Number int    `json:"number"`
	URL    string `json:"url"`
}

// Service validates, redacts, and submits one report through the proxy.
type Service struct {
	Endpoint string
	SiteKey  string
	Version  string
	Platform string
	Client   HTTPDoer
	Logs     func(context.Context) ([]LogEntry, error)
	Now      func() time.Time
	// EndpointProvider 可选；非空时每次 Submit 都会先问一次当前地址。
	EndpointProvider EndpointProvider
}

// New builds the production feedback service.
func New(version string) *Service {
	endpoint := strings.TrimSpace(os.Getenv("QKA_FEEDBACK_URL"))
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	return &Service{
		Endpoint: endpoint,
		SiteKey:  DefaultSiteKey,
		Version:  version,
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
		Client:   newHTTPClient(),
		Now:      time.Now,
	}
}

// Preview returns a redacted count and excerpt of the recent error logs.
func (s *Service) Preview(ctx context.Context) (Context, error) {
	view := Context{
		Available:  strings.TrimSpace(s.Endpoint) != "",
		Notice:     "文本和日志会在发送前脱敏；图片中可见的敏感信息无法自动识别，请先遮盖。",
		MaxImages:  maxImages,
		MaxImageMB: maxImageBytes / (1024 * 1024),
	}
	entries, err := s.logs(ctx)
	if err != nil {
		return view, err
	}
	lines := formatLogs(entries, s.now())
	view.LogCount = len(lines)
	if len(lines) > 12 {
		lines = lines[len(lines)-12:]
	}
	view.Excerpt = strings.Join(lines, "\n")
	return view, nil
}

// Submit redacts the report and proxies it to the Worker. The Worker creates
// one issue and commits attachments in a single feedback-assets branch commit.
func (s *Service) Submit(ctx context.Context, req Request) (Result, error) {
	var result Result
	// effectiveEndpoint 保证非空（最后回落到 DefaultEndpoint），这里不需要
	// 再判空；地址是否可用由下面的 url.Parse 与 scheme 校验决定。
	endpointURL := s.effectiveEndpoint()

	description := SanitizeText(req.Description, maxDescriptionRunes)
	if utf8.RuneCountInString(description) < 6 {
		return result, fmt.Errorf("%w: 请至少填写 6 个字符", ErrInvalidInput)
	}
	if len(req.Images) > maxImages {
		return result, fmt.Errorf("%w: 最多 %d 张图片", ErrInvalidInput, maxImages)
	}
	images := make([]Image, 0, len(req.Images))
	total := 0
	for _, image := range req.Images {
		raw, mime, err := decodeImage(image.DataURL)
		if err != nil {
			return result, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
		total += len(raw)
		if total > maxTotalImageBytes {
			return result, fmt.Errorf("%w: 图片总大小超过限制", ErrInvalidInput)
		}
		images = append(images, Image{
			Name:    "screenshot",
			DataURL: "data:image/" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw),
		})
	}

	logText := ""
	if req.IncludeLogs {
		entries, err := s.logs(ctx)
		if err != nil {
			return result, fmt.Errorf("feedback: 读取异常日志失败: %w", err)
		}
		logText = strings.Join(formatLogs(entries, s.now()), "\n")
		if len(logText) > maxLogsBytes {
			logText = truncateBytes(logText, maxLogsBytes)
		}
	}

	title := issueTitle(description)
	// Every payload field has a JSON-safe concrete type, so marshaling this
	// struct cannot fail.
	body, _ := json.Marshal(struct {
		Title       string  `json:"title"`
		Description string  `json:"description"`
		Logs        string  `json:"logs,omitempty"`
		Images      []Image `json:"images,omitempty"`
		Version     string  `json:"version"`
		Platform    string  `json:"platform"`
	}{
		Title:       title,
		Description: description,
		Logs:        logText,
		Images:      images,
		Version:     SanitizeText(s.Version, 80),
		Platform:    SanitizeText(s.Platform, 80),
	})
	if len(body) > 9<<20 {
		return result, fmt.Errorf("%w: 反馈内容过大", ErrInvalidInput)
	}

	endpoint, err := url.Parse(endpointURL)
	if err != nil || (endpoint.Scheme != "https" && !isLoopbackEndpoint(endpoint)) {
		return result, fmt.Errorf("%w: 反馈服务地址无效", ErrEndpointUnavailable)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", "Quota-Keepalive/"+safeHeaderValue(s.Version))
	httpReq.Header.Set("X-QKA-Key", s.SiteKey)

	resp, err := s.client().Do(httpReq)
	if err != nil {
		return result, fmt.Errorf("feedback: %s", describeNetworkError(err))
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if readErr != nil {
		return result, readErr
	}
	if len(data) > maxResponseBytes {
		return result, errors.New("feedback: 服务响应过大")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var problem struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &problem)
		message := SanitizeText(problem.Error, 240)
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return result, fmt.Errorf("feedback: HTTP %d: %s", resp.StatusCode, message)
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return result, fmt.Errorf("feedback: 服务响应无法解析: %w", err)
	}
	if !result.OK || result.Number <= 0 || result.URL == "" {
		return result, errors.New("feedback: 服务未返回 Issue 链接")
	}
	if _, err := url.ParseRequestURI(result.URL); err != nil ||
		!strings.HasPrefix(result.URL, "https://github.com/") {
		return result, errors.New("feedback: 服务返回了无效链接")
	}
	return result, nil
}

func (s *Service) logs(ctx context.Context) ([]LogEntry, error) {
	if s.Logs == nil {
		return nil, nil
	}
	entries, err := s.Logs(ctx)
	if err != nil {
		return nil, err
	}
	if len(entries) > maxLogLines {
		entries = entries[len(entries)-maxLogLines:]
	}
	return entries, nil
}

// bodyIsWorkerMissing 判断响应体是否是 Cloudflare 的「该主机名没有 Worker」
// 错误页。判定独立出来是为了可测：只看 HTTP 状态码会把未部署当成可达。
func bodyIsWorkerMissing(body string) bool {
	for _, code := range []string{"error code: 1042", "error code: 1014", "error code: 1033"} {
		if strings.Contains(body, code) {
			return true
		}
	}
	return false
}

// effectiveEndpoint 优先用调用方（设置页）给出的自定义地址，其次用
// Endpoint（含 QKA_FEEDBACK_URL 覆盖），最后回落到内置地址。
func (s *Service) effectiveEndpoint() string {
	if s.EndpointProvider != nil {
		if custom := strings.TrimSpace(s.EndpointProvider()); custom != "" {
			return custom
		}
	}
	if custom := strings.TrimSpace(s.Endpoint); custom != "" {
		return custom
	}
	return DefaultEndpoint
}

// newHTTPClient 给反馈提交单独设定超时：连接阶段很快就会失败（DNS 被污染或
// 端口不通时没必要让用户干等 35 秒），但保留足够的总时长给多图上传。
func newHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{
		Timeout:   6 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	transport.TLSHandshakeTimeout = 6 * time.Second
	transport.ResponseHeaderTimeout = 20 * time.Second
	return &http.Client{Timeout: 25 * time.Second, Transport: transport}
}

// describeNetworkError 把底层网络错误翻译成用户能照着处理的说法。
// 反馈服务部署在 workers.dev，该域名在部分网络下会被解析到不可达地址，
// 表现为长时间 i/o timeout——这里要能明确指出是网络/域名问题而不是内容问题。
func describeNetworkError(err error) string {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		// NXDOMAIN 与「网络不通」是两回事：前者说明服务端根本没部署到这个域名，
		// 让用户换网络永远修不好，必须把结论说准。
		if dnsErr.IsNotFound {
			return "反馈服务地址不存在（该服务可能尚未部署），请联系维护者"
		}
		return "反馈服务域名解析失败，请检查网络或稍后重试"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "连接反馈服务超时（当前网络可能无法访问该地址），请换网络或配置自定义反馈地址"
	}
	return "网络异常: " + err.Error()
}

func (s *Service) client() HTTPDoer {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{Timeout: 35 * time.Second}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func formatLogs(entries []LogEntry, now time.Time) []string {
	lines := make([]string, 0, len(entries))
	cutoff := now.Add(-48 * time.Hour)
	for _, entry := range entries {
		if ts, err := time.Parse(time.RFC3339, entry.TS); err == nil && ts.Before(cutoff) {
			continue
		}
		level := strings.ToLower(strings.TrimSpace(entry.Level))
		state := strings.ToLower(strings.TrimSpace(entry.State))
		if level != "error" && level != "warn" && level != "warning" &&
			state != "failed_retryable" && state != "failed_final" &&
			state != "manual_required" {
			continue
		}
		line := strings.TrimSpace(strings.Join([]string{
			SanitizeText(entry.TS, 64),
			"[" + SanitizeText(firstNonEmpty(level, "error"), 24) + "]",
			SanitizeText(entry.Target, 80),
			SanitizeText(maskAccount(entry.AccountID), 80),
			SanitizeText(entry.Operation, 48),
			SanitizeText(entry.State, 48),
			SanitizeText(entry.Message, 1000),
		}, " "))
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func issueTitle(description string) string {
	line := strings.TrimSpace(strings.SplitN(description, "\n", 2)[0])
	line = strings.ReplaceAll(line, "@", "@\u200b")
	line = strings.ReplaceAll(line, "#", `\#`)
	line = SanitizeText(line, 72)
	if line == "" {
		line = "用户反馈"
	}
	return "[用户反馈] " + line
}

func maskAccount(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "—"
	}
	runes := []rune(value)
	if len(runes) <= 4 {
		return "****"
	}
	return "****" + string(runes[len(runes)-4:])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func truncateBytes(value string, max int) string {
	if len(value) <= max {
		return value
	}
	data := []byte(value)
	data = data[:max]
	for !utf8.Valid(data) && len(data) > 0 {
		data = data[:len(data)-1]
	}
	return string(data) + "\n…"
}

func safeHeaderValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, value)
	if value == "" {
		return "dev"
	}
	return value
}

func isLoopbackEndpoint(endpoint *url.URL) bool {
	if endpoint == nil || endpoint.Scheme != "http" {
		return false
	}
	host := endpoint.Hostname()
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

var dataURLPattern = regexp.MustCompile(`^data:image/(jpeg|jpg|png|webp);base64,([A-Za-z0-9+/]*={0,2})$`)

func decodeImage(dataURL string) ([]byte, string, error) {
	match := dataURLPattern.FindStringSubmatch(strings.TrimSpace(dataURL))
	if match == nil {
		return nil, "", errors.New("图片格式必须是 PNG、JPEG 或 WebP")
	}
	raw, err := base64.StdEncoding.DecodeString(match[2])
	if err != nil {
		return nil, "", errors.New("图片 Base64 无法解析")
	}
	if len(raw) == 0 {
		return nil, "", errors.New("图片内容为空")
	}
	if len(raw) > maxImageBytes {
		return nil, "", fmt.Errorf("单张图片超过 %d MB", maxImageBytes/(1024*1024))
	}
	mime := match[1]
	if mime == "jpg" {
		mime = "jpeg"
	}
	actual := detectImageMIME(raw)
	if actual == "" || actual != mime {
		return nil, "", errors.New("图片内容与声明的格式不一致")
	}
	return raw, mime, nil
}

func detectImageMIME(raw []byte) string {
	switch {
	case len(raw) >= 3 && raw[0] == 0xff && raw[1] == 0xd8 && raw[2] == 0xff:
		return "jpeg"
	case len(raw) >= 8 && bytes.Equal(raw[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}):
		return "png"
	case len(raw) >= 12 && bytes.Equal(raw[:4], []byte("RIFF")) && bytes.Equal(raw[8:12], []byte("WEBP")):
		return "webp"
	default:
		return ""
	}
}
