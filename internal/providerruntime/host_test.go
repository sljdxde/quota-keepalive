package providerruntime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func launchFixture(t *testing.T, extraEnv ...string) *Host {
	t.Helper()
	cfg := helperConfig(t, extraEnv...)
	host, err := Launch(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close() })
	return host
}

func TestLaunchValidation(t *testing.T) {
	ctx := context.Background()
	cases := []LaunchConfig{
		{},
		{Bun: "bun"},
		{Bun: "bun", ConfigDir: "x"},
		{Bun: "bun", AuthPath: "y"},
	}
	for _, cfg := range cases {
		if _, err := Launch(ctx, cfg); err == nil {
			t.Fatalf("invalid config should fail: %+v", cfg)
		}
	}
}

func TestLaunchDefaultsDirectoryAndPassesPluginOptions(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "init.json")
	cfg := LaunchConfig{
		Bun:       helperExecutable(t),
		ConfigDir: dir,
		AuthPath:  filepath.Join(dir, "auth.json"),
		Env:       helperEnv("HELPER_CAPTURE=" + capture),
		Plugins: []Plugin{{
			Spec: "@magpie-community/opencode-x-auth", Target: dir,
			Options: map[string]any{"region": "cn"},
		}},
	}
	host, err := Launch(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"region":"cn"`)) ||
		!bytes.Contains(data, []byte(`"plugins"`)) {
		t.Fatalf("plugin options were not sent to host: %s", data)
	}
}

func TestLaunchConfigDirectoryPermissionFailure(t *testing.T) {
	parent := t.TempDir()
	file := filepath.Join(parent, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Launch(context.Background(), LaunchConfig{
		Bun: helperExecutable(t), ConfigDir: file, AuthPath: file,
	})
	if err == nil {
		t.Fatal("file config path should fail")
	}
}

func TestLaunchInitExitAndPluginDiagnostics(t *testing.T) {
	cfg := helperConfig(t, "HELPER_EXIT_INIT=1")
	if _, err := Launch(context.Background(), cfg); err == nil {
		t.Fatal("host exiting during init must fail")
	}

	host := launchFixture(t, "HELPER_PLUGIN_ERROR=1")
	plugins := host.LoadedPlugins()
	if len(plugins) != 1 || plugins[0].Error != "manifest failed" {
		t.Fatalf("loaded plugin diagnostics mismatch: %+v", plugins)
	}
	plugins[0].Error = "mutated"
	if host.LoadedPlugins()[0].Error != "manifest failed" {
		t.Fatal("LoadedPlugins must return a copy")
	}
}

func TestHostProviderLoginUsageAndCheckin(t *testing.T) {
	ctx := context.Background()
	host := launchFixture(t)
	providers, err := host.Providers(ctx)
	if err != nil || len(providers) != 1 {
		t.Fatalf("providers: %+v %v", providers, err)
	}
	if providers[0].Accounts[0].AccountID != "user@example.com" ||
		providers[0].Models[0].ID != "fixture-model" {
		t.Fatalf("provider fields not decoded: %+v", providers[0])
	}
	prompt, err := host.Prompt(ctx, "fixture", 0, map[string]string{"a": "b"})
	if err != nil || prompt.Key != "" {
		t.Fatalf("empty prompt: %+v %v", prompt, err)
	}
	authorized, err := host.Authorize(ctx, "fixture", 0, nil, NewAccount)
	if err != nil || authorized.Session != "session-1" {
		t.Fatalf("authorize: %+v %v", authorized, err)
	}
	saved, err := host.Finish(ctx, "session-1", "code")
	if err != nil || saved.Provider != "fixture" {
		t.Fatalf("finish: %+v %v", saved, err)
	}
	saved, err = host.APIKey(ctx, "fixture", 1, nil, "key", NewAccount)
	if err != nil || saved.Account != "a1" {
		t.Fatalf("api key: %+v %v", saved, err)
	}
	usage, err := host.Usage(ctx, "fixture", "a1")
	if err != nil || usage.Plan != "Pro" || len(usage.Windows) != 1 ||
		usage.Windows[0].Balance == nil || *usage.Windows[0].Balance != 12.5 {
		t.Fatalf("usage: %+v %v", usage, err)
	}
	checkin, err := host.Checkin(ctx, "fixture", "a1")
	if err != nil || checkin.Outcome != "claimed" || checkin.Credit != 100 {
		t.Fatalf("checkin: %+v %v", checkin, err)
	}
	loader, err := host.Load(ctx, "fixture", "a1")
	if err != nil || loader.APIKey != "secret" || !loader.Fetch {
		t.Fatalf("load: %+v %v", loader, err)
	}
	if err := host.SignOut(ctx, "fixture", "a1"); err != nil {
		t.Fatal(err)
	}
}

func TestHostPromptAndLoginFailures(t *testing.T) {
	ctx := context.Background()
	host := launchFixture(t, "HELPER_PROMPT=1")
	prompt, err := host.Prompt(ctx, "fixture", 0, nil)
	if err != nil || prompt.Key != "phone" || prompt.Placeholder == "" {
		t.Fatalf("prompt: %+v %v", prompt, err)
	}
	_ = host.Close()

	failing := launchFixture(t, "HELPER_FAIL_LOGIN=1")
	if _, err := failing.Finish(ctx, "s", "bad"); err == nil ||
		!strings.Contains(err.Error(), "bad code") {
		t.Fatalf("callback error: %v", err)
	}
	if _, err := failing.APIKey(ctx, "fixture", 1, nil, "bad", NewAccount); err == nil ||
		!strings.Contains(err.Error(), "bad key") {
		t.Fatalf("api key error: %v", err)
	}
}

func TestHostFetchStreamingAndErrors(t *testing.T) {
	ctx := context.Background()
	capture := t.TempDir() + "/fetch.json"
	host := launchFixture(t, "HELPER_CAPTURE_FETCH="+capture)
	response, err := host.Fetch(ctx, FetchRequest{
		Provider: "fixture", Account: "a1", URL: "https://provider.example/v1/chat",
		Body: `{"model":"fixture-model"}`,
	})
	if err != nil || response.Status != 200 ||
		!bytes.Contains(response.Body, []byte("OK")) {
		t.Fatalf("fetch: %+v %v", response, err)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(wire.Body)
	if err != nil || string(decoded) != `{"model":"fixture-model"}` {
		t.Fatalf("fetch body must be base64 on the wire: %q %v", decoded, err)
	}

	invalid := launchFixture(t, "HELPER_INVALID_CHUNK=1")
	if _, err := invalid.Fetch(ctx, FetchRequest{
		Provider: "fixture", Account: "a1", URL: "https://provider.example",
	}); !errors.Is(err, ErrProtocol) {
		t.Fatalf("invalid chunk must be protocol error, got %v", err)
	}

	empty := launchFixture(t, "HELPER_FETCH_BODY=empty")
	response, err = empty.Fetch(ctx, FetchRequest{
		Provider: "fixture", Account: "a1", URL: "https://provider.example",
	})
	if err != nil || !bytes.Equal(response.Body, []byte(`{}`)) {
		t.Fatalf("empty body fetch: %+v %v", response, err)
	}

	hung := launchFixture(t, "HELPER_FETCH_HANG=1")
	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	if _, err := hung.Fetch(timeoutCtx, FetchRequest{
		Provider: "fixture", Account: "a1", URL: "https://provider.example",
	}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("hung fetch should time out, got %v", err)
	}

	failing := launchFixture(t, "HELPER_FETCH_ERROR=1")
	if _, err := failing.Fetch(ctx, FetchRequest{
		Provider: "fixture", Account: "a1", URL: "https://provider.example",
	}); err == nil || !strings.Contains(err.Error(), "fetch denied") {
		t.Fatalf("fetch error: %v", err)
	}

	oversized := launchFixture(t, "HELPER_FETCH_OVERSIZED=1")
	if _, err := oversized.Fetch(ctx, FetchRequest{
		Provider: "fixture", Account: "a1", URL: "https://provider.example",
	}); !errors.Is(err, ErrProtocol) {
		t.Fatalf("oversized fetch must be protocol error: %v", err)
	}
}

func TestHostProtocolEventAndBadJSON(t *testing.T) {
	eventHost := launchFixture(t, "HELPER_EVENT_THEN_RESULT=1")
	if _, err := eventHost.Providers(context.Background()); err != nil {
		t.Fatalf("event before result should be ignored: %v", err)
	}

	badHost := launchFixture(t, "HELPER_BAD_PROVIDERS=1")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := badHost.Providers(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("malformed JSON should not complete call: %v", err)
	}
}

func TestReadBoundedLine(t *testing.T) {
	reader := bufio.NewReaderSize(strings.NewReader(strings.Repeat("x", 128)+"\n"), 16)
	line, err := readBoundedLine(reader, 256)
	if err != nil || len(line) != 129 {
		t.Fatalf("line read: %d %v", len(line), err)
	}
	reader = bufio.NewReaderSize(strings.NewReader(strings.Repeat("x", 300)+"\n"), 16)
	if _, err := readBoundedLine(reader, 128); !errors.Is(err, ErrProtocol) {
		t.Fatalf("oversized line: %v", err)
	}
	reader = bufio.NewReaderSize(strings.NewReader("partial"), 16)
	line, err = readBoundedLine(reader, 128)
	if err == nil || string(line) != "partial" {
		t.Fatalf("partial line: %q %v", line, err)
	}
}

func TestHostAuthFilesAndCloseAreIdempotent(t *testing.T) {
	host := launchFixture(t)
	auth := host.config.AuthPath
	if err := os.WriteFile(auth, []byte(`{"x":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := host.ReadAuth()
	if err != nil || string(data) != `{"x":1}` {
		t.Fatalf("ReadAuth: %s %v", data, err)
	}
	if err := host.RemoveAuth(); err != nil {
		t.Fatal(err)
	}
	if err := host.RemoveAuth(); err != nil {
		t.Fatal(err)
	}
	if _, err := host.ReadAuth(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing auth: %v", err)
	}
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := host.Providers(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("closed host should reject calls: %v", err)
	}
}

func TestRemoveAuthFilesErrorBranches(t *testing.T) {
	if err := RemoveAuthFiles(" \t"); err == nil {
		t.Fatal("empty auth path should fail")
	}

	root := t.TempDir()
	parentFile := filepath.Join(root, "file")
	if err := os.WriteFile(parentFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAuthFiles(filepath.Join(parentFile, "auth.json")); err == nil {
		t.Fatal("non-directory auth parent should fail")
	}
	if err := RemoveAuthFiles(filepath.Join(root, "missing", "auth.json")); err != nil {
		t.Fatalf("missing auth directory should be ignored: %v", err)
	}

	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	if err := os.Mkdir(filepath.Join(dir, "auth.json.bak"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAuthFiles(authPath); err != nil {
		t.Fatalf("directories must be skipped: %v", err)
	}

	if err := os.WriteFile(authPath, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	removeErr := errors.New("remove failed")
	old := removeAuthPath
	removeAuthPath = func(path string) error {
		if filepath.Base(path) == "auth.json" {
			return removeErr
		}
		return os.Remove(path)
	}
	t.Cleanup(func() { removeAuthPath = old })
	if err := RemoveAuthFiles(authPath); !errors.Is(err, removeErr) {
		t.Fatalf("remove error = %v", err)
	}
}

func TestClosedHostRejectsAllOperations(t *testing.T) {
	host := launchFixture(t)
	_ = host.Close()
	ctx := context.Background()
	if _, err := host.Prompt(ctx, "fixture", 0, nil); !errors.Is(err, ErrNotRunning) {
		t.Fatal(err)
	}
	if _, err := host.Finish(ctx, "s", "c"); !errors.Is(err, ErrNotRunning) {
		t.Fatal(err)
	}
	if _, err := host.APIKey(ctx, "fixture", 0, nil, "k", "a"); !errors.Is(err, ErrNotRunning) {
		t.Fatal(err)
	}
	if _, err := host.Fetch(ctx, FetchRequest{}); !errors.Is(err, ErrNotRunning) {
		t.Fatal(err)
	}
}

func TestDispatchPushDropsWhenFull(t *testing.T) {
	h := &Host{pending: map[int64]*hostCall{}}
	h.dispatch(message{ID: 0})
	h.dispatch(message{ID: 99})

	full := &hostCall{ch: make(chan message, 256)}
	for i := 0; i < 256; i++ {
		full.ch <- message{ID: int64(i)}
	}
	full.push(message{ID: 999})
	if len(full.ch) != 256 {
		t.Fatal("full push should drop without blocking")
	}
	closed := &hostCall{ch: make(chan message, 1)}
	closed.closed.Store(true)
	closed.push(message{ID: 1})
	if len(closed.ch) != 0 {
		t.Fatal("closed call must ignore pushes")
	}
}

func TestBeginExitedWithoutExitError(t *testing.T) {
	h := &Host{pending: map[int64]*hostCall{}, exited: true}
	if _, _, err := h.begin(); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("begin: %v", err)
	}
}

func TestBoundedBuffer(t *testing.T) {
	var b boundedBuffer
	first := bytes.Repeat([]byte("a"), 20<<10)
	if n, err := b.Write(first); err != nil || n != len(first) {
		t.Fatal(n, err)
	}
	second := bytes.Repeat([]byte("b"), 20<<10)
	if n, err := b.Write(second); err != nil || n != len(second) {
		t.Fatal(n, err)
	}
	if len(b.buf) != 32<<10 {
		t.Fatalf("bounded buffer grew to %d", len(b.buf))
	}
	if n, err := b.Write([]byte("ignored")); err != nil || n != len("ignored") {
		t.Fatal(n, err)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if firstNonEmpty("", "", "x") != "x" || firstNonEmpty("", "") != "" {
		t.Fatal("firstNonEmpty mismatch")
	}
}

func TestHostWriteFailure(t *testing.T) {
	h := &Host{
		stdin: closedWriter{}, pending: map[int64]*hostCall{},
	}
	if err := h.send(1, "x", nil); err == nil {
		t.Fatal("closed writer should fail")
	}
	if err := h.send(1, "x", map[string]any{"bad": func() {}}); err == nil {
		t.Fatal("unmarshalable params should fail")
	}
	if err := h.call(context.Background(), "x", nil, nil); err == nil {
		t.Fatal("call write failure should return")
	}
	if _, err := h.Fetch(context.Background(), FetchRequest{}); err == nil {
		t.Fatal("fetch write failure should return")
	}
}

type closedWriter struct{}

func (closedWriter) Write([]byte) (int, error) { return 0, os.ErrClosed }
func (closedWriter) Close() error              { return nil }

type bufferWriteCloser struct{ bytes.Buffer }

func (*bufferWriteCloser) Close() error { return nil }

func TestHostCallSkipsEventsAndForwardsErrors(t *testing.T) {
	h := &Host{
		stdin: &bufferWriteCloser{}, pending: map[int64]*hostCall{},
	}
	done := make(chan error, 1)
	go func() {
		done <- h.call(context.Background(), "empty", nil, nil)
	}()
	var call *hostCall
	for i := 0; i < 100 && call == nil; i++ {
		h.mu.Lock()
		call = h.pending[1]
		h.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	if call == nil {
		t.Fatal("call did not register")
	}
	call.push(message{Event: "progress"})
	call.push(message{Error: &struct {
		Message string `json:"message"`
	}{Message: "upstream error"}})
	err := <-done
	if err == nil || !strings.Contains(err.Error(), "upstream error") {
		t.Fatalf("call error: %v", err)
	}
}

func TestHostCallContextCancellation(t *testing.T) {
	cfg := helperConfig(t, "HELPER_HANG_ALL=1")
	host, err := Launch(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := host.Prompt(ctx, "fixture", 0, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("call should time out: %v", err)
	}
}

func TestHostConcurrentClose(t *testing.T) {
	host := launchFixture(t)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = host.Close()
		}()
	}
	wg.Wait()
}

func TestCloseWakesPendingAndWaitLoopSuccess(t *testing.T) {
	call := &hostCall{ch: make(chan message, 1)}
	h := &Host{
		stdin: closedWriter{}, pending: map[int64]*hostCall{1: call},
		cmd: exec.Command("/usr/bin/true"),
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if len(call.ch) != 1 {
		t.Fatal("Close should wake pending calls")
	}

	success := &Host{cmd: exec.Command("/usr/bin/true"), pending: map[int64]*hostCall{}}
	if err := success.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	success.waitLoop()
	if !errors.Is(success.exitErr, ErrNotRunning) {
		t.Fatalf("successful exit should map to ErrNotRunning: %v", success.exitErr)
	}
}

func TestHostStartFailure(t *testing.T) {
	cfg := helperConfig(t)
	cfg.Bun = filepath.Join(t.TempDir(), "missing-bun")
	if _, err := Launch(context.Background(), cfg); err == nil {
		t.Fatal("missing Bun should fail")
	}
}

func TestLaunchHostScriptWriteFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "provider-host.js"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Launch(context.Background(), LaunchConfig{
		Bun: "bun", ConfigDir: dir, AuthPath: filepath.Join(dir, "auth.json"),
	}); err == nil {
		t.Fatal("host.js 写入失败应返回")
	}
}

func TestLaunchPipeFailures(t *testing.T) {
	cases := []struct {
		name string
		cmd  func() *exec.Cmd
	}{
		{
			name: "stdin",
			cmd: func() *exec.Cmd {
				return &exec.Cmd{Stdin: os.Stdin}
			},
		},
		{
			name: "stdout",
			cmd: func() *exec.Cmd {
				return &exec.Cmd{Stdout: os.Stdout}
			},
		},
		{
			name: "stderr",
			cmd: func() *exec.Cmd {
				return &exec.Cmd{Stderr: os.Stderr}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			old := newCommand
			t.Cleanup(func() { newCommand = old })
			newCommand = func(string, ...string) *exec.Cmd { return tc.cmd() }
			dir := t.TempDir()
			if _, err := Launch(context.Background(), LaunchConfig{
				Bun: "bun", ConfigDir: dir, AuthPath: filepath.Join(dir, "auth.json"),
			}); err == nil {
				t.Fatalf("%s pipe 失败应返回", tc.name)
			}
		})
	}
}
