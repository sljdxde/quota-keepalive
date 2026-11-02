package feedback

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSanitizeTextRedactsCredentialsAndIdentity(t *testing.T) {
	input := strings.Join([]string{
		"Authorization: Bearer abcdefghijklmnop",
		"access_token=secret-value&xsec_token=token-value",
		"email alice@example.com phone 13812345678",
		"path /Users/yuzhou/project",
		strings.Repeat("a", 60),
	}, "\n")
	got := SanitizeText(input, 0)
	for _, forbidden := range []string{
		"abcdefghijklmnop", "secret-value", "token-value",
		"alice@example.com", "13812345678", "yuzhou", strings.Repeat("a", 60),
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("sanitized text still contains %q:\n%s", forbidden, got)
		}
	}
	for _, want := range []string{"[redacted]", "a***@example.com", "138****5678", "/Users/<user>"} {
		if !strings.Contains(got, want) {
			t.Errorf("sanitized text missing %q:\n%s", want, got)
		}
	}
}

func TestPreviewUsesRecentRedactedErrorLogs(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	service := &Service{
		Endpoint: "https://feedback.example.test/api/feedback",
		Now:      func() time.Time { return now },
		Logs: func(context.Context) ([]LogEntry, error) {
			return []LogEntry{
				{TS: now.Add(-72 * time.Hour).Format(time.RFC3339), Level: "error", Message: "expired"},
				{TS: now.Add(-time.Minute).Format(time.RFC3339), Level: "info", State: "completed", Message: "ok"},
				{TS: now.Add(-time.Minute).Format(time.RFC3339), Level: "error", AccountID: "account-9283", Target: "workbuddy", Operation: "refresh", State: "failed_final", Message: "token=secret"},
			}, nil
		},
	}
	view, err := service.Preview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !view.Available || view.LogCount != 1 {
		t.Fatalf("unexpected preview: %+v", view)
	}
	if strings.Contains(view.Excerpt, "secret") || strings.Contains(view.Excerpt, "account-9283") {
		t.Fatalf("preview leaked sensitive data: %s", view.Excerpt)
	}
	if !strings.Contains(view.Excerpt, "****9283") {
		t.Fatalf("preview did not mask account: %s", view.Excerpt)
	}
}

func TestSubmitRejectsInvalidImage(t *testing.T) {
	service := &Service{Endpoint: "https://feedback.example.test/api/feedback"}
	_, err := service.Submit(context.Background(), Request{
		Description: "界面出现问题",
		Images:      []Image{{DataURL: "data:image/svg+xml;base64,PHN2Zz4="}},
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput, got %v", err)
	}
}

func TestSubmitRedactsAndPostsPayload(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-QKA-Key"); got != "test-key" {
			t.Errorf("missing site key: %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"ok":true,"number":42,"url":"https://github.com/sljdxde/quota-keepalive/issues/42"}`))
	}))
	defer server.Close()

	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 1, 2, 3}
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	service := &Service{
		Endpoint: server.URL,
		SiteKey:  "test-key",
		Version:  "1.0.0",
		Platform: "darwin/arm64",
		Logs: func(context.Context) ([]LogEntry, error) {
			return []LogEntry{{
				TS: time.Now().Format(time.RFC3339), Level: "error", Target: "provider",
				AccountID: "user@example.com", Operation: "refresh", State: "failed_final",
				Message: "api_key=SECRET",
			}}, nil
		},
	}
	result, err := service.Submit(context.Background(), Request{
		Description: "刷新失败，邮箱 user@example.com，api_key=SECRET",
		IncludeLogs: true,
		Images:      []Image{{DataURL: dataURL}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Number != 42 || !strings.HasSuffix(result.URL, "/issues/42") {
		t.Fatalf("unexpected result: %+v", result)
	}
	encoded, _ := json.Marshal(received)
	text := string(encoded)
	for _, forbidden := range []string{"SECRET", "user@example.com"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("request leaked %q: %s", forbidden, text)
		}
	}
	if !strings.Contains(text, "data:image/png;base64,") {
		t.Fatalf("request missing image: %s", text)
	}
}

func TestSubmitReportsProxyFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"请求过于频繁"}`))
	}))
	defer server.Close()
	service := &Service{Endpoint: server.URL, SiteKey: "test"}
	_, err := service.Submit(context.Background(), Request{Description: "界面出现问题"})
	if err == nil || !strings.Contains(err.Error(), "请求过于频繁") {
		t.Fatalf("unexpected error: %v", err)
	}
}
