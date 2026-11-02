package feedback

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

type failingReadCloser struct{}

func (failingReadCloser) Read([]byte) (int, error) { return 0, errors.New("read failed") }
func (failingReadCloser) Close() error             { return nil }

func TestFeedbackServiceConstructionAndLogLimits(t *testing.T) {
	t.Setenv("QKA_FEEDBACK_URL", "https://feedback.example.test/api")
	service := New("1.0.0")
	if service.Endpoint != "https://feedback.example.test/api" ||
		service.Platform == "" || service.Client == nil || service.Now == nil {
		t.Fatalf("unexpected service: %+v", service)
	}
	t.Setenv("QKA_FEEDBACK_URL", "")
	if New("1").Endpoint != DefaultEndpoint {
		t.Fatal("empty override should use default endpoint")
	}

	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	logs := make([]LogEntry, 90)
	for index := range logs {
		logs[index] = LogEntry{
			TS:    now.Add(time.Duration(index) * time.Second).Format(time.RFC3339),
			Level: "error", Message: "failure",
		}
	}
	preview, err := (&Service{
		Endpoint: "https://feedback.example.test/api",
		Now:      func() time.Time { return now },
		Logs: func(context.Context) ([]LogEntry, error) {
			return logs, nil
		},
	}).Preview(context.Background())
	if err != nil || preview.LogCount != 80 || strings.Count(preview.Excerpt, "\n") != 11 {
		t.Fatalf("preview limits: %+v %v", preview, err)
	}
	if _, err := (&Service{
		Endpoint: "https://feedback.example.test/api",
		Logs: func(context.Context) ([]LogEntry, error) {
			return nil, errors.New("logs unavailable")
		},
	}).Preview(context.Background()); err == nil {
		t.Fatal("preview log error should surface")
	}
	if lines, err := (&Service{}).logs(context.Background()); err != nil || lines != nil {
		t.Fatalf("nil log source = %v %v", lines, err)
	}
	if (&Service{}).client() == nil || (&Service{}).now().IsZero() {
		t.Fatal("defaults should be usable")
	}
}

func TestSubmitValidationAndTransportFailures(t *testing.T) {
	ctx := context.Background()
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 1}
	pngURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)

	checks := []struct {
		name    string
		service *Service
		req     Request
	}{
		{"empty endpoint", &Service{}, Request{Description: "long enough"}},
		{"short description", &Service{Endpoint: "https://feedback.example.test"}, Request{Description: "短"}},
		{"too many images", &Service{Endpoint: "https://feedback.example.test"}, Request{
			Description: "long enough", Images: make([]Image, maxImages+1),
		}},
		{"logs error", &Service{
			Endpoint: "https://feedback.example.test",
			Logs: func(context.Context) ([]LogEntry, error) {
				return nil, errors.New("logs")
			},
		}, Request{Description: "long enough", IncludeLogs: true}},
		{"bad endpoint", &Service{Endpoint: "http://example.test"}, Request{Description: "long enough"}},
		{"network error", &Service{
			Endpoint: "https://feedback.example.test",
			Client: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("offline")
			}),
		}, Request{Description: "long enough"}},
	}
	for _, check := range checks {
		if _, err := check.service.Submit(ctx, check.req); err == nil {
			t.Errorf("%s should fail", check.name)
		}
	}

	if _, err := (&Service{
		Endpoint: "https://feedback.example.test",
		Client: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: failingReadCloser{}}, nil
		}),
	}).Submit(ctx, Request{Description: "long enough"}); err == nil {
		t.Fatal("body read failure should surface")
	}
	if _, err := (&Service{
		Endpoint: "https://feedback.example.test",
		Client: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewReader(make([]byte, maxResponseBytes+1))),
			}, nil
		}),
	}).Submit(ctx, Request{Description: "long enough"}); err == nil {
		t.Fatal("oversized response should fail")
	}

	for _, tc := range []struct {
		status int
		body   string
	}{
		{http.StatusBadRequest, "not-json"},
		{http.StatusOK, "not-json"},
		{http.StatusOK, `{"ok":false}`},
		{http.StatusOK, `{"ok":true,"number":1,"url":"http://evil.test/issue"}`},
	} {
		service := &Service{
			Endpoint: "https://feedback.example.test",
			Client: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: tc.status,
					Body:       io.NopCloser(strings.NewReader(tc.body)),
					Header:     make(http.Header),
				}, nil
			}),
		}
		if _, err := service.Submit(ctx, Request{Description: "long enough"}); err == nil {
			t.Errorf("status %d body %q should fail", tc.status, tc.body)
		}
	}

	valid := pngURL
	if _, err := (&Service{
		Endpoint: "https://feedback.example.test",
		Client: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"ok":true,"number":2,"url":"https://github.com/o/r/issues/2"}`)),
			}, nil
		}),
		Logs: func(context.Context) ([]LogEntry, error) {
			return []LogEntry{{TS: time.Now().Format(time.RFC3339), Level: "error", Message: "x"}}, nil
		},
	}).Submit(ctx, Request{
		Description: "long enough", IncludeLogs: true, Images: []Image{{DataURL: valid}},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestImageBudgetAndLogTruncation(t *testing.T) {
	large := append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a},
		make([]byte, 1300*1024)...)
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(large)
	images := make([]Image, 6)
	for index := range images {
		images[index] = Image{DataURL: dataURL}
	}
	_, err := (&Service{Endpoint: "https://feedback.example.test"}).Submit(context.Background(), Request{
		Description: "long enough", Images: images,
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("total image budget: %v", err)
	}

	payloadImage := append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a},
		make([]byte, maxImageBytes-8)...)
	payloadURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(payloadImage)
	payloadImages := make([]Image, 5)
	for index := range payloadImages {
		payloadImages[index] = Image{DataURL: payloadURL}
	}
	_, err = (&Service{Endpoint: "https://feedback.example.test"}).Submit(context.Background(), Request{
		Description: "筛选下拉框在深色模式下看不清，重新打开后仍然存在。",
		Images:      payloadImages,
	})
	if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "内容过大") {
		t.Fatalf("oversized request payload: %v", err)
	}

	logs := make([]LogEntry, maxLogLines)
	for index := range logs {
		logs[index] = LogEntry{
			TS: time.Now().Format(time.RFC3339), Level: "error",
			Message: strings.Repeat("异常", 1000),
		}
	}
	service := &Service{
		Endpoint: "https://feedback.example.test",
		Logs:     func(context.Context) ([]LogEntry, error) { return logs, nil },
		Client: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(req.Body)
			if !bytes.Contains(body, []byte("…")) {
				t.Error("long logs should be visibly truncated")
			}
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"ok":true,"number":3,"url":"https://github.com/o/r/issues/3"}`)),
			}, nil
		}),
	}
	if _, err := service.Submit(context.Background(), Request{
		Description: "long enough", IncludeLogs: true,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := service.Submit(nil, Request{Description: "long enough"}); err == nil {
		t.Fatal("nil context should be rejected before the network request")
	}
}

func TestFeedbackHelpersAndImageDetection(t *testing.T) {
	if issueTitle("") != "[用户反馈] 用户反馈" {
		t.Fatal("empty title fallback missing")
	}
	if firstNonEmpty() != "" || firstNonEmpty("", "fallback") != "fallback" {
		t.Fatal("firstNonEmpty fallback contract changed")
	}
	if maskAccount("") != "—" || maskAccount("abc") != "****" ||
		maskAccount("account-1234") != "****1234" {
		t.Fatal("account masking incomplete")
	}
	if safeHeaderValue("\x00 \t") != "dev" || safeHeaderValue(" 1.0 ") != "1.0" {
		t.Fatal("header sanitization incomplete")
	}
	if isLoopbackEndpoint(nil) || isLoopbackEndpoint(urlForTest{Scheme: "https", Host: "localhost"}.URL()) ||
		!isLoopbackEndpoint(urlForTest{Scheme: "http", Host: "127.0.0.1"}.URL()) ||
		isLoopbackEndpoint(urlForTest{Scheme: "http", Host: "example.test"}.URL()) {
		t.Fatal("loopback detection incomplete")
	}
	if got := truncateBytes("hello", 10); got != "hello" {
		t.Fatal("short truncate")
	}
	if got := truncateBytes("你好", 1); !strings.HasSuffix(got, "…") || !utf8.ValidString(got) {
		t.Fatalf("utf8 truncate = %q", got)
	}

	jpeg := []byte{0xff, 0xd8, 0xff, 1}
	if _, mime, err := decodeImage("data:image/jpg;base64," + base64.StdEncoding.EncodeToString(jpeg)); err != nil || mime != "jpeg" {
		t.Fatalf("jpeg detection = %q %v", mime, err)
	}
	webp := append([]byte("RIFF1234WEBP"), 1)
	if detectImageMIME(webp) != "webp" {
		t.Fatal("webp detection")
	}
	if detectImageMIME([]byte("plain")) != "" {
		t.Fatal("unknown image should be rejected")
	}
	if _, _, err := decodeImage("data:image/png;base64,"); err == nil {
		t.Fatal("empty image should be rejected")
	}
	if _, _, err := decodeImage("data:image/png;base64,A"); err == nil {
		t.Fatal("invalid base64 should be rejected")
	}
	if _, _, err := decodeImage("data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("no"))); err == nil {
		t.Fatal("mismatched magic should be rejected")
	}
	if _, _, err := decodeImage("data:image/png;base64," + base64.StdEncoding.EncodeToString(
		append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, make([]byte, maxImageBytes)...))); err == nil {
		t.Fatal("oversized image should be rejected")
	}
}

func TestSanitizeTextControlLimitAndPlatformPaths(t *testing.T) {
	got := SanitizeText("a\x00b\r\nc\r"+strings.Repeat("x", 10), 5)
	if strings.ContainsAny(got[:1], "\x00") || !strings.HasSuffix(got, "…") {
		t.Fatalf("sanitized text = %q", got)
	}
	for _, value := range []string{"/home/alice/a", `C:\Users\alice\a`} {
		if redacted := SanitizeText(value, 0); strings.Contains(redacted, "alice") {
			t.Errorf("path leaked: %s", redacted)
		}
	}
}

func TestFormatLogsDefaultsMissingLevelForFailedState(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	lines := formatLogs([]LogEntry{{
		TS: now.Format(time.RFC3339), State: "failed_final", Message: "network failed",
	}}, now)
	if len(lines) != 1 || !strings.Contains(lines[0], "[error]") {
		t.Fatalf("failed state should default to error level: %v", lines)
	}
}

type urlForTest struct {
	Scheme string
	Host   string
}

func (u urlForTest) URL() *url.URL {
	return &url.URL{Scheme: u.Scheme, Host: u.Host}
}
