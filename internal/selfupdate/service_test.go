package selfupdate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

type closeErrorWriter struct{}

func (closeErrorWriter) Write(p []byte) (int, error) { return len(p), nil }
func (closeErrorWriter) Close() error                { return errors.New("close failed") }

func response(status int, body io.Reader) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(body),
		Header:     make(http.Header),
	}
}

func releaseFeed(version, platform, name, artifactURL, hash string, includeArtifacts bool) []byte {
	stable := map[string]any{
		"version": version,
		"url":     artifactURL,
		"name":    name,
		"sha256":  hash,
	}
	if includeArtifacts {
		stable["artifacts"] = []map[string]any{{
			"platform": platform,
			"name":     name,
			"url":      artifactURL,
			"sha256":   hash,
		}}
	}
	data, _ := json.Marshal(map[string]any{"stable": stable})
	return data
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestNewUsesEnvironmentAndDefaults(t *testing.T) {
	t.Setenv("QKA_UPDATE_FEED_URL", " https://updates.example/feed.json ")
	t.Setenv("QKA_UPDATE_MIRROR", " https://mirror.example ")
	svc := New("1.0.0", "/tmp/qka-config")
	if svc.FeedURL != "https://updates.example/feed.json" {
		t.Fatalf("FeedURL = %q", svc.FeedURL)
	}
	if svc.Mirror != "https://mirror.example" {
		t.Fatalf("Mirror = %q", svc.Mirror)
	}
	if svc.StateDir != filepath.Join("/tmp/qka-config", "updates") {
		t.Fatalf("StateDir = %q", svc.StateDir)
	}
	if svc.feedURL() != svc.FeedURL || svc.platform() == "" || svc.client() == nil {
		t.Fatal("default service dependencies are not initialized")
	}
	empty := &Service{}
	if empty.feedURL() != DefaultFeedURL {
		t.Fatal("empty FeedURL must fall back to the official feed")
	}
	if empty.client() == nil {
		t.Fatal("nil Client must create a bounded HTTP client")
	}
}

func TestAppendMirrorValidationAndFallback(t *testing.T) {
	tests := []struct {
		name   string
		url    string
		mirror string
		want   string
	}{
		{name: "https", url: "https://official.example/a/b", mirror: "https://mirror.example",
			want: "https://mirror.example/a/b"},
		{name: "http", url: "http://official.example/a", mirror: "http://mirror.example/",
			want: "http://mirror.example/a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := applyMirror(tt.url, tt.mirror)
			if err != nil || got != tt.want {
				t.Fatalf("applyMirror = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
	for _, tt := range []struct {
		url, mirror string
	}{
		{"ftp://host/a", "https://mirror"},
		{"https://host", "https://mirror"},
		{"https://host/a", "mirror"},
		{"https://host/a", "https://mirror/path"},
	} {
		if _, err := applyMirror(tt.url, tt.mirror); !errors.Is(err, ErrBadMirror) {
			t.Fatalf("applyMirror(%q,%q) err = %v", tt.url, tt.mirror, err)
		}
	}
}

func TestWaitIdleImmediateDefaultAndRollback(t *testing.T) {
	if err := WaitIdle(context.Background(), func() bool { return true }, 0); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := WaitIdle(ctx, func() bool { return false }, time.Microsecond); err == nil {
		t.Fatal("expired context must fail")
	}
	if got := RollbackPath("/Applications/QKA.app"); got != filepath.Join("/Applications/QKA.app", "qka.old") {
		t.Fatalf("RollbackPath = %q", got)
	}
}

func TestCheckSuccessAndArtifactSelection(t *testing.T) {
	artifact := []byte("release")
	feed := releaseFeed("1.2.0", "darwin/arm64", "QKA.dmg",
		"https://updates.example/QKA.dmg", digest(artifact), true)
	var gotURL string
	svc := &Service{
		CurrentVersion: "1.1.0",
		FeedURL:        "https://updates.example/feed.json",
		StateDir:       t.TempDir(),
		Platform:       "darwin/arm64",
		Client: doerFunc(func(req *http.Request) (*http.Response, error) {
			gotURL = req.URL.String()
			if req.Header.Get("Accept") != "application/json" {
				t.Error("Check must request JSON")
			}
			return response(http.StatusOK, bytes.NewReader(feed)), nil
		}),
	}
	status, err := svc.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotURL != svc.FeedURL || !status.Available || status.LatestVersion != "1.2.0" ||
		status.Artifact != "QKA.dmg" || status.CurrentVersion != "1.1.0" {
		t.Fatalf("unexpected status: %+v url=%s", status, gotURL)
	}
}

func TestSelectReleaseAppliesMirrorAfterSelection(t *testing.T) {
	feed := releaseFeed("2.0.0", "test/os", "QKA.dmg",
		"https://download.example/QKA.dmg", "hash", true)
	release, err := selectRelease(feed, "test/os", "1.0.0", "https://mirror.example")
	if err != nil {
		t.Fatal(err)
	}
	if release.URL != "https://mirror.example/QKA.dmg" {
		t.Fatalf("release URL = %q", release.URL)
	}
}

func TestCheckNoUpdateAndNoArtifact(t *testing.T) {
	current := runtime.GOOS + "/" + runtime.GOARCH
	tests := []struct {
		name string
		feed []byte
		want error
		msg  string
	}{
		{
			name: "same version",
			feed: releaseFeed("1.0.0", current, "QKA", "https://u/QKA", "hash", false),
			msg:  "当前已是最新版本",
		},
		{
			name: "missing platform",
			feed: releaseFeed("2.0.0", "plan9/mips", "QKA", "https://u/QKA", "hash", true),
			want: ErrNoArtifact,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &Service{
				CurrentVersion: "1.0.0", FeedURL: "https://u/feed", StateDir: t.TempDir(),
				Client: doerFunc(func(*http.Request) (*http.Response, error) {
					return response(http.StatusOK, bytes.NewReader(tt.feed)), nil
				}),
			}
			status, err := svc.Check(context.Background())
			if tt.want != nil {
				if !errors.Is(err, tt.want) {
					t.Fatalf("err = %v, want %v", err, tt.want)
				}
				return
			}
			if err != nil || status.Available || status.Message != tt.msg {
				t.Fatalf("status=%+v err=%v", status, err)
			}
		})
	}
}

func TestCheckFailures(t *testing.T) {
	tests := []struct {
		name   string
		client HTTPDoer
	}{
		{
			name: "request error",
			client: doerFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("network")
			}),
		},
		{
			name: "http status",
			client: doerFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusServiceUnavailable, strings.NewReader("")), nil
			}),
		},
		{
			name: "read error",
			client: doerFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusOK, failingReader{}), nil
			}),
		},
		{
			name: "oversized",
			client: doerFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusOK, bytes.NewReader(make([]byte, maxFeedBytes+1))), nil
			}),
		},
		{
			name: "invalid json",
			client: doerFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusOK, strings.NewReader("{")), nil
			}),
		},
		{
			name: "bad mirror",
			client: doerFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusOK, bytes.NewReader(releaseFeed(
					"2.0.0", "x", "QKA", "https://u/QKA", "hash", false))), nil
			}),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &Service{
				CurrentVersion: "1.0.0", FeedURL: "https://u/feed", StateDir: t.TempDir(),
				Client: tt.client, Mirror: "https://mirror/path",
			}
			if _, err := svc.Check(context.Background()); err == nil {
				t.Fatal("expected failure")
			}
		})
	}

	badURL := &Service{
		CurrentVersion: "1.0.0", FeedURL: "://bad", StateDir: t.TempDir(),
		Client: doerFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("invalid request must not reach client")
			return nil, nil
		}),
	}
	if _, err := badURL.Check(context.Background()); err == nil {
		t.Fatal("invalid feed URL must fail")
	}
}

func TestReleaseFromFeedFailures(t *testing.T) {
	tests := []struct {
		name   string
		url    string
		client HTTPDoer
	}{
		{
			name: "bad URL", url: "://bad",
			client: doerFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("bad URL must not reach client")
				return nil, nil
			}),
		},
		{
			name: "request", url: "https://u/feed",
			client: doerFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("network")
			}),
		},
		{
			name: "status", url: "https://u/feed",
			client: doerFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusForbidden, strings.NewReader("")), nil
			}),
		},
		{
			name: "read", url: "https://u/feed",
			client: doerFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusOK, failingReader{}), nil
			}),
		},
		{
			name: "oversized", url: "https://u/feed",
			client: doerFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusOK, bytes.NewReader(make([]byte, maxFeedBytes+1))), nil
			}),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &Service{FeedURL: tt.url, Client: tt.client}
			if _, _, err := svc.fetchRelease(context.Background()); err == nil {
				t.Fatal("expected release feed failure")
			}
		})
	}
}

func TestDownloadAndApplySuccess(t *testing.T) {
	artifact := []byte("signed application")
	hash := digest(artifact)
	feed := releaseFeed("1.2.0", "test/os", "QKA-test.dmg",
		"https://download.example/QKA-test.dmg", hash, true)
	stateDir := t.TempDir()
	const feedURL = "https://updates.example/feed"
	appBundle := filepath.Join(t.TempDir(), "QKA.app")
	executable := filepath.Join(appBundle, "Contents", "MacOS", "QKA")
	if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	var installerTarget, installerPath string
	feedCalls := 0
	svc := &Service{
		CurrentVersion: "1.0.0",
		FeedURL:        feedURL,
		StateDir:       stateDir,
		Platform:       "test/os",
		Executable:     func() (string, error) { return executable, nil },
		Client: doerFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.String() {
			case feedURL:
				feedCalls++
				return response(http.StatusOK, bytes.NewReader(feed)), nil
			case "https://download.example/QKA-test.dmg":
				return response(http.StatusOK, bytes.NewReader(artifact)), nil
			default:
				t.Fatalf("unexpected URL %s", req.URL)
				return nil, nil
			}
		}),
		StartInstaller: func(_ context.Context, target, path string) error {
			installerTarget, installerPath = target, path
			return nil
		},
	}
	status, err := svc.DownloadAndApply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Available || !status.Downloaded || !status.Ready ||
		status.LatestVersion != "1.2.0" || status.Artifact != "QKA-test.dmg" {
		t.Fatalf("unexpected status: %+v", status)
	}
	if feedCalls != 1 {
		t.Fatalf("update feed reads = %d, want 1", feedCalls)
	}
	if installerTarget == "" || installerPath != filepath.Join(stateDir, "QKA-test.dmg") {
		t.Fatalf("installer handoff target=%q path=%q", installerTarget, installerPath)
	}
	got, err := os.ReadFile(installerPath)
	if err != nil || !bytes.Equal(got, artifact) {
		t.Fatalf("staged artifact = %q err=%v", got, err)
	}
	staged, err := os.ReadFile(filepath.Join(stateDir, "staged.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved stagedRelease
	if err := json.Unmarshal(staged, &saved); err != nil || saved.Path != installerPath {
		t.Fatalf("staged metadata: %+v err=%v", saved, err)
	}
	if status2, err := svc.Check(context.Background()); err != nil || !status2.Ready {
		t.Fatalf("staged status not restored: %+v %v", status2, err)
	}
}

func TestDownloadAndApplyFailures(t *testing.T) {
	artifact := []byte("artifact")
	hash := digest(artifact)
	feed := releaseFeed("2.0.0", "test/os", "QKA.dmg",
		"https://download.example/QKA.dmg", hash, true)
	newService := func() *Service {
		return &Service{
			CurrentVersion: "1.0.0", FeedURL: "https://updates.example/feed",
			StateDir: t.TempDir(), Platform: "test/os",
			Executable: func() (string, error) { return "/tmp/QKA", nil },
			Client: doerFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.String() == "https://updates.example/feed" {
					return response(http.StatusOK, bytes.NewReader(feed)), nil
				}
				return response(http.StatusOK, bytes.NewReader(artifact)), nil
			}),
			StartInstaller: func(context.Context, string, string) error { return nil },
		}
	}

	t.Run("no update", func(t *testing.T) {
		svc := &Service{
			CurrentVersion: "3.0.0", FeedURL: "https://u/feed", StateDir: t.TempDir(),
			Client: doerFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusOK, bytes.NewReader(releaseFeed(
					"2.0.0", "test/os", "QKA", "https://d/QKA", "hash", false))), nil
			}),
		}
		if _, err := svc.DownloadAndApply(context.Background()); !errors.Is(err, ErrNoUpdate) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("installer dependency", func(t *testing.T) {
		svc := newService()
		svc.StartInstaller = nil
		if _, err := svc.DownloadAndApply(context.Background()); err == nil {
			t.Fatal("missing installer must fail")
		}
	})
	t.Run("executable", func(t *testing.T) {
		svc := newService()
		svc.Executable = func() (string, error) { return "", errors.New("executable") }
		if _, err := svc.DownloadAndApply(context.Background()); err == nil {
			t.Fatal("executable error must fail")
		}
	})
	t.Run("installer", func(t *testing.T) {
		svc := newService()
		appBundle := filepath.Join(t.TempDir(), "QKA.app")
		executable := filepath.Join(appBundle, "Contents", "MacOS", "QKA")
		if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
			t.Fatal(err)
		}
		svc.Executable = func() (string, error) { return executable, nil }
		svc.StartInstaller = func(context.Context, string, string) error {
			return errors.New("installer")
		}
		if _, err := svc.DownloadAndApply(context.Background()); err == nil {
			t.Fatal("installer error must fail")
		}
	})
	t.Run("check", func(t *testing.T) {
		svc := newService()
		svc.Client = doerFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("feed")
		})
		if _, err := svc.DownloadAndApply(context.Background()); err == nil {
			t.Fatal("feed check error must fail")
		}
	})
	t.Run("download", func(t *testing.T) {
		svc := newService()
		svc.Client = doerFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() == "https://updates.example/feed" {
				return response(http.StatusOK, bytes.NewReader(feed)), nil
			}
			return response(http.StatusBadGateway, strings.NewReader("")), nil
		})
		if _, err := svc.DownloadAndApply(context.Background()); err == nil {
			t.Fatal("download error must fail")
		}
	})
}

func TestDownloadValidationFailures(t *testing.T) {
	artifact := []byte("artifact")
	tests := []struct {
		name     string
		release  Release
		stateDir func(*testing.T) string
		client   HTTPDoer
	}{
		{
			name:    "missing metadata",
			release: Release{Version: "2.0.0"},
		},
		{
			name:    "missing state dir",
			release: Release{Version: "2.0.0", URL: "https://d/QKA", SHA256: "x"},
		},
		{
			name:    "invalid download url",
			release: Release{Version: "2.0.0", URL: "http://bad\n", SHA256: "x"},
		},
		{
			name:    "download status",
			release: Release{Version: "2.0.0", URL: "https://d/QKA", SHA256: "x"},
			client: doerFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusBadGateway, strings.NewReader("")), nil
			}),
		},
		{
			name:    "hash mismatch",
			release: Release{Version: "2.0.0", URL: "https://d/QKA", Name: "QKA", SHA256: "bad"},
			client: doerFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusOK, bytes.NewReader(artifact)), nil
			}),
		},
		{
			name:    "directory target",
			release: Release{Version: "2.0.0", URL: "https://d/QKA", Name: "QKA", SHA256: digest(artifact)},
			stateDir: func(t *testing.T) string {
				dir := t.TempDir()
				if err := os.Mkdir(filepath.Join(dir, "QKA"), 0o700); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			client: doerFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusOK, bytes.NewReader(artifact)), nil
			}),
		},
		{
			name:    "part target is directory",
			release: Release{Version: "2.0.0", URL: "https://d/QKA", Name: "QKA", SHA256: digest(artifact)},
			stateDir: func(t *testing.T) string {
				dir := t.TempDir()
				if err := os.Mkdir(filepath.Join(dir, "QKA.part"), 0o700); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			client: doerFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusOK, bytes.NewReader(artifact)), nil
			}),
		},
		{
			name:    "copy error",
			release: Release{Version: "2.0.0", URL: "https://d/QKA", Name: "QKA", SHA256: "x"},
			client: doerFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusOK, failingReader{}), nil
			}),
		},
		{
			name:    "state path is file",
			release: Release{Version: "2.0.0", URL: "https://d/QKA", Name: "QKA", SHA256: "x"},
			stateDir: func(t *testing.T) string {
				file := filepath.Join(t.TempDir(), "not-a-dir")
				if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
				return filepath.Join(file, "updates")
			},
			client: doerFunc(func(*http.Request) (*http.Response, error) {
				return response(http.StatusOK, bytes.NewReader(artifact)), nil
			}),
		},
		{
			name:    "download request error",
			release: Release{Version: "2.0.0", URL: "https://d/QKA", SHA256: "x"},
			client: doerFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("network")
			}),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stateDir := ""
			if tt.stateDir != nil {
				stateDir = tt.stateDir(t)
			} else if tt.name != "missing state dir" {
				stateDir = t.TempDir()
			}
			svc := &Service{StateDir: stateDir, Client: tt.client}
			if _, err := svc.download(context.Background(), tt.release); err == nil {
				t.Fatal("expected download failure")
			}
		})
	}
}

func TestDownloadStagedWriteFailure(t *testing.T) {
	artifact := []byte("artifact")
	stateDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(stateDir, "staged.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	svc := &Service{
		StateDir: stateDir,
		Client: doerFunc(func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, bytes.NewReader(artifact)), nil
		}),
	}
	if _, err := svc.download(context.Background(), Release{
		Version: "2.0.0", URL: "https://d/QKA", Name: "QKA", SHA256: digest(artifact),
	}); err == nil {
		t.Fatal("staged metadata write failure must be returned")
	}
}

func TestDownloadOversizedAndCloseFailure(t *testing.T) {
	oldMax := maxUpdateBytes
	maxUpdateBytes = 1
	t.Cleanup(func() { maxUpdateBytes = oldMax })
	svc := &Service{
		StateDir: t.TempDir(),
		Client: doerFunc(func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, strings.NewReader("xx")), nil
		}),
	}
	if _, err := svc.download(context.Background(), Release{
		Version: "2.0.0", URL: "https://d/QKA", Name: "QKA", SHA256: digest([]byte("xx")),
	}); err == nil {
		t.Fatal("oversized update must fail")
	}

	oldOpen := openUpdateFile
	openUpdateFile = func(string) (io.WriteCloser, error) { return closeErrorWriter{}, nil }
	t.Cleanup(func() { openUpdateFile = oldOpen })
	if _, err := svc.download(context.Background(), Release{
		Version: "2.0.0", URL: "https://d/QKA", Name: "QKA", SHA256: digest([]byte("xx")),
	}); err == nil {
		t.Fatal("close error must fail")
	}
}

func TestStagedStatusValidation(t *testing.T) {
	stateDir := t.TempDir()
	svc := &Service{StateDir: stateDir}
	if status := svc.status(); status.CurrentVersion != "dev" || status.Message != "尚未检查更新" {
		t.Fatalf("empty status: %+v", status)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "staged.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status := svc.status(); status.Ready {
		t.Fatal("invalid staged metadata must not be ready")
	}
	if err := os.WriteFile(filepath.Join(stateDir, "staged.json"),
		[]byte(`{"version":"2.0.0","path":"/missing","name":"QKA"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if status := svc.status(); status.Ready {
		t.Fatal("missing staged artifact must not be ready")
	}
}

func TestStagedUpdateAppliesWithoutFeedAndRejectsStaleVersion(t *testing.T) {
	stateDir := t.TempDir()
	artifactPath := filepath.Join(stateDir, "QKA-test.dmg")
	if err := os.WriteFile(artifactPath, []byte("artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	staged := stagedRelease{
		Release: Release{
			Version: "2.0.0",
			Name:    "QKA-test.dmg",
			URL:     "https://updates.example/QKA-test.dmg",
			SHA256:  "hash",
		},
		Path: artifactPath,
	}
	data, err := json.Marshal(staged)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "staged.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	bundle := filepath.Join(t.TempDir(), "QKA.app")
	executable := filepath.Join(bundle, "Contents", "MacOS", "QKA")
	if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	var installerPath string
	svc := &Service{
		CurrentVersion: "1.0.0",
		FeedURL:        "https://updates.example/feed",
		StateDir:       stateDir,
		Executable:     func() (string, error) { return executable, nil },
		Client: doerFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("staged update must not access the network")
			return nil, nil
		}),
		StartInstaller: func(_ context.Context, _, path string) error {
			installerPath = path
			return nil
		},
	}
	status, err := svc.Check(context.Background())
	if err != nil || !status.Ready {
		t.Fatalf("staged Check status=%+v err=%v", status, err)
	}
	ready, err := svc.DownloadAndApply(context.Background())
	if err != nil || !ready.Ready || installerPath != artifactPath {
		t.Fatalf("staged apply status=%+v path=%q err=%v", ready, installerPath, err)
	}

	stale := staged
	stale.Version = "1.0.0"
	data, err = json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "staged.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if status := svc.status(); status.Ready {
		t.Fatal("a staged version not newer than the running version must not be ready")
	}
}

func TestApplyStagedRejectsMissingMetadata(t *testing.T) {
	svc := &Service{StateDir: t.TempDir()}
	if _, err := svc.applyStaged(context.Background(), Status{}); !errors.Is(err, ErrNotStaged) {
		t.Fatalf("missing staged metadata err = %v", err)
	}
}

func TestStagedReleaseRejectsMissingAndDirectoryArtifacts(t *testing.T) {
	stateDir := t.TempDir()
	writeStaged := func(path string) {
		t.Helper()
		data, err := json.Marshal(stagedRelease{
			Release: Release{Version: "2.0.0", Name: "QKA.dmg"},
			Path:    path,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stateDir, "staged.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	svc := &Service{CurrentVersion: "1.0.0", StateDir: stateDir}
	writeStaged(filepath.Join(stateDir, "missing.dmg"))
	if _, ok := svc.stagedRelease(); ok {
		t.Fatal("missing staged artifact must be rejected")
	}
	directory := filepath.Join(stateDir, "artifact.dmg")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeStaged(directory)
	if _, ok := svc.stagedRelease(); ok {
		t.Fatal("directory staged artifact must be rejected")
	}
}

func TestPathWithinRejectsOutsideArtifact(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "updates", "QKA.dmg")
	outside := filepath.Join(t.TempDir(), "QKA.dmg")
	if !pathWithin(root, inside) {
		t.Fatal("artifact inside state directory must be accepted")
	}
	if pathWithin(root, outside) || pathWithin(root, root) {
		t.Fatal("artifact outside state directory must be rejected")
	}
}

func TestPathWithinHandlesAbsFailures(t *testing.T) {
	old := absUpdatePath
	t.Cleanup(func() { absUpdatePath = old })

	absUpdatePath = func(string) (string, error) { return "", errors.New("root") }
	if pathWithin("root", "candidate") {
		t.Fatal("root absolute path failure must reject")
	}

	calls := 0
	absUpdatePath = func(value string) (string, error) {
		calls++
		if calls == 1 {
			return value, nil
		}
		return "", errors.New("candidate")
	}
	if pathWithin("root", "candidate") {
		t.Fatal("candidate absolute path failure must reject")
	}
}
