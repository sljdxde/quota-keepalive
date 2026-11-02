package gui

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"qka/internal/feedback"
)

func TestFeedbackBindingsPreviewAndSubmit(t *testing.T) {
	unwired := NewBoundService(Deps{})
	if _, err := unwired.FeedbackContext(); !errors.Is(err, errNotWired) {
		t.Fatalf("FeedbackContext without deps = %v", err)
	}
	if _, err := unwired.FeedbackSubmit("描述内容足够", nil, false); !errors.Is(err, errNotWired) {
		t.Fatalf("FeedbackSubmit without deps = %v", err)
	}

	previewService := &feedback.Service{
		Endpoint: "https://feedback.example.test",
		Logs: func(context.Context) ([]feedback.LogEntry, error) {
			return []feedback.LogEntry{{
				TS: time.Now().Format(time.RFC3339), Level: "error",
				AccountID: "account-9283", Message: "token=secret",
			}}, nil
		},
	}
	preview, err := NewBoundService(Deps{Feedback: previewService}).FeedbackContext()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(preview), "****9283") ||
		strings.Contains(string(preview), "account-9283") ||
		strings.Contains(string(preview), "secret") {
		t.Fatalf("feedback preview was not safely redacted: %s", preview)
	}

	previewService.Logs = func(context.Context) ([]feedback.LogEntry, error) {
		return nil, errors.New("logs unavailable")
	}
	if _, err := NewBoundService(Deps{Feedback: previewService}).FeedbackContext(); err == nil {
		t.Fatal("feedback preview log error should surface")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-QKA-Key") == "" {
			t.Error("site key header missing")
		}
		_, _ = w.Write([]byte(`{"ok":true,"number":17,"url":"https://github.com/sljdxde/quota-keepalive/issues/17"}`))
	}))
	defer server.Close()
	submitService := &feedback.Service{Endpoint: server.URL, SiteKey: "test"}
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 1}
	raw, err := NewBoundService(Deps{Feedback: submitService}).FeedbackSubmit(
		"设置页下拉框显示异常，需要反馈。",
		[]feedback.Image{{Name: "screen.png", DataURL: "data:image/png;base64," +
			base64.StdEncoding.EncodeToString(png)}},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"number":17`) {
		t.Fatalf("feedback result = %s", raw)
	}

	server.Close()
	if _, err := NewBoundService(Deps{Feedback: submitService}).FeedbackSubmit(
		"设置页下拉框显示异常，需要反馈。", nil, false,
	); err == nil {
		t.Fatal("feedback network failure should surface")
	}
}
