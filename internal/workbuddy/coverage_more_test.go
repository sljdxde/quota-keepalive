package workbuddy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"qka/internal/capabilities"
)

func TestIgnoreMissingPendingCredentialBranches(t *testing.T) {
	if err := ignoreMissingPendingCredential(capabilities.ErrSecretNotFound); err != nil {
		t.Fatalf("missing platform secret should be ignored: %v", err)
	}
	want := errors.New("delete failed")
	if err := ignoreMissingPendingCredential(want); !errors.Is(err, want) {
		t.Fatalf("other delete errors should surface: %v", err)
	}
}

func TestNoteCleanupFailureSkipsStaleSession(t *testing.T) {
	svc := NewLoginService(LoginConfig{})
	svc.noteCleanupFailure("workbuddy-cn", "missing")
	installLoginSession(svc, "current", LoginPending)
	svc.noteCleanupFailure("workbuddy-cn", "old")
	if got := svc.Status(context.Background(), "workbuddy-cn"); got.Status != LoginPending {
		t.Fatalf("stale cleanup must not mutate current view: %+v", got)
	}
}

func TestLoginPollCleanupFailuresAreVisible(t *testing.T) {
	t.Run("context canceled after save", func(t *testing.T) {
		target := coverageSuccessLoginServer(t)
		defer target.Close()
		ctx, cancel := context.WithCancel(context.Background())
		store := &hookPendingStore{
			save: func(context.Context, string, []byte) error {
				cancel()
				return nil
			},
			delete: func(context.Context, string) error {
				return errors.New("delete failed")
			},
		}
		svc := NewLoginService(LoginConfig{BaseURL: target.URL, PendingStore: store})
		installLoginSession(svc, "cancel-after-save", LoginPending)
		svc.poll(ctx, "workbuddy-cn", "cancel-after-save", "state")
		got := svc.Status(context.Background(), "workbuddy-cn")
		if got.Status != LoginFailed || !strings.Contains(got.Message, "临时登录凭据清理失败") {
			t.Fatalf("cleanup failure should be visible: %+v", got)
		}
	})

	t.Run("session replaced and cleanup fails", func(t *testing.T) {
		target := coverageSuccessLoginServer(t)
		defer target.Close()
		started := make(chan struct{})
		release := make(chan struct{})
		store := &hookPendingStore{
			save: func(context.Context, string, []byte) error {
				close(started)
				<-release
				return nil
			},
			delete: func(context.Context, string) error {
				return errors.New("delete failed")
			},
		}
		svc := NewLoginService(LoginConfig{BaseURL: target.URL, PendingStore: store})
		installLoginSession(svc, "old-session", LoginPending)
		done := make(chan struct{})
		go func() {
			defer close(done)
			svc.poll(context.Background(), "workbuddy-cn", "old-session", "state")
		}()
		<-started
		svc.mu.Lock()
		svc.sessions["workbuddy-cn"] = &loginSession{
			id: "new-session",
			view: LoginView{
				Target: "workbuddy-cn", Status: LoginPending, Message: "new",
			},
		}
		svc.mu.Unlock()
		close(release)
		<-done
	})
}

func coverageSuccessLoginServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/plugin/auth/token":
			writeEnvelope(t, w, map[string]any{
				"accessToken": "access", "refreshToken": "refresh", "expiresIn": 3600,
			})
		case "/v2/plugin/login/account":
			writeEnvelope(t, w, map[string]any{"uid": "uid"})
		default:
			http.NotFound(w, r)
		}
	}))
}
