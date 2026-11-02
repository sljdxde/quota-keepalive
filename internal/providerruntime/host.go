// Package providerruntime runs OpenCode v1 provider plugins under Bun using
// Magpie's provider host protocol. QKA owns persistence and policy; the host
// only performs provider actions for one short-lived process.
package providerruntime

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"qka/internal/atomicfile"
)

//go:embed js/host.js
var hostJS []byte

var (
	newCommand     = exec.Command
	removeAuthPath = os.Remove
)

const maxLineBytes = 32 << 20

var (
	// ErrNotRunning reports an operation before host startup.
	ErrNotRunning = errors.New("providerruntime: host is not running")
	// ErrProtocol reports a malformed or oversized host response.
	ErrProtocol = errors.New("providerruntime: protocol error")
)

// NewAccount asks the provider host to create a new account slot.
const NewAccount = "new"

// Plugin is one local provider plugin passed to host.js.
type Plugin struct {
	Spec    string         `json:"spec"`
	Target  string         `json:"target"`
	Options map[string]any `json:"options,omitempty"`
}

// LoadedPlugin reports whether one bundled plugin initialized and, when it
// did not, a diagnostic that is safe to show in the provider management UI.
type LoadedPlugin struct {
	Spec  string `json:"spec"`
	Error string `json:"error,omitempty"`
}

// Method describes a provider sign-in method.
type Method struct {
	Type        string `json:"type"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder,omitempty"`
	Keyless     bool   `json:"keyless,omitempty"`
}

// Model is a provider model list entry. Only stable, non-secret fields are
// retained.
type Model struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	NPM     string  `json:"npm"`
	APIURL  string  `json:"url"`
	Context int     `json:"context"`
	Free    bool    `json:"free"`
	Rate    float64 `json:"rate"`
	RateWas float64 `json:"rateWas"`
}

// Account is one provider account. Key is a stable account selector and must
// never be used as a display identity.
type Account struct {
	Key       string   `json:"key"`
	Type      string   `json:"type"`
	AccountID string   `json:"accountId"`
	Hint      string   `json:"hint,omitempty"`
	Models    []string `json:"models,omitempty"`
}

// Provider is the credential-free projection returned by host.js.
type Provider struct {
	ID        string    `json:"id"`
	Spec      string    `json:"spec"`
	Name      string    `json:"name"`
	Icon      string    `json:"icon,omitempty"`
	Usage     bool      `json:"usage"`
	Checkin   bool      `json:"checkin,omitempty"`
	Fetch     bool      `json:"fetch,omitempty"`
	SignedIn  bool      `json:"signedIn"`
	AuthType  string    `json:"authType"`
	NPM       string    `json:"npm,omitempty"`
	APIURL    string    `json:"api,omitempty"`
	AccountID string    `json:"accountId"`
	Single    bool      `json:"single,omitempty"`
	Methods   []Method  `json:"methods"`
	Models    []Model   `json:"models"`
	Accounts  []Account `json:"accounts"`
}

// Authorization is the browser-auth start result.
type Authorization struct {
	Session      string `json:"session"`
	URL          string `json:"url"`
	Instructions string `json:"instructions"`
	Method       string `json:"method"`
	Opened       bool   `json:"opened"`
}

// Saved is where a completed sign-in was stored.
type Saved struct {
	Provider string `json:"provider"`
	Account  string `json:"account"`
}

// Prompt is the next sign-in prompt when a method asks for inputs.
type Prompt struct {
	Type        string `json:"type"`
	Key         string `json:"key"`
	Message     string `json:"message"`
	Placeholder string `json:"placeholder,omitempty"`
	Options     []struct {
		Label string `json:"label"`
		Value string `json:"value"`
		Hint  string `json:"hint,omitempty"`
	} `json:"options,omitempty"`
}

// UsageWindow is a whitelisted quota window.
type UsageWindow struct {
	Name      string   `json:"name"`
	Used      *float64 `json:"used"`
	ResetsAt  string   `json:"resetsAt"`
	Display   string   `json:"display"`
	Amount    float64  `json:"amount"`
	Limit     float64  `json:"limit"`
	Unit      string   `json:"unit"`
	Model     string   `json:"model"`
	Models    []string `json:"models"`
	NotModels []string `json:"notModels"`
	Aside     bool     `json:"aside"`
	Balance   *float64 `json:"balance"`
}

// Usage is the provider's whitelisted allowance projection.
type Usage struct {
	Plan    string        `json:"plan"`
	Until   string        `json:"until"`
	Balance string        `json:"balance"`
	Error   string        `json:"error"`
	User    string        `json:"user"`
	SignIn  string        `json:"signIn"`
	Windows []UsageWindow `json:"windows"`
}

// Checkin is a provider's daily check-in outcome.
type Checkin struct {
	Outcome string  `json:"outcome"`
	Credit  float64 `json:"credit"`
	Streak  int     `json:"streak"`
	Message string  `json:"message"`
	Until   string  `json:"until"`
}

// Loader describes the request path exposed by a provider sign-in.
type Loader struct {
	BaseURL string            `json:"baseURL"`
	APIKey  string            `json:"apiKey"`
	Headers map[string]string `json:"headers"`
	Fetch   bool              `json:"fetch"`
}

// FetchRequest is a bounded signed request executed inside the provider host.
type FetchRequest struct {
	Provider string            `json:"provider"`
	Account  string            `json:"account"`
	URL      string            `json:"url"`
	Method   string            `json:"method"`
	Headers  map[string]string `json:"headers"`
	Body     string            `json:"body"`
	Window   int               `json:"window"`
}

// FetchResponse is a bounded, credential-free signed request result.
type FetchResponse struct {
	Status  int
	Headers map[string]string
	Body    []byte
}

type message struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
	Event   string            `json:"event"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Data    string            `json:"data"`
}

type hostCall struct {
	ch     chan message
	closed atomic.Bool
}

// Host is one Bun provider host process.
type Host struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	config LaunchConfig

	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  int64
	pending map[int64]*hostCall
	exited  bool
	exitErr error
	stderr  boundedBuffer
	closed  atomic.Bool
	loaded  []LoadedPlugin
}

// LaunchConfig configures a provider host.
type LaunchConfig struct {
	Bun        string
	ConfigDir  string
	AuthPath   string
	Directory  string
	Plugins    []Plugin
	Env        []string
	LoadMillis int
	ListMillis int
}

// Launch starts host.js and completes its init RPC.
func Launch(ctx context.Context, cfg LaunchConfig) (*Host, error) {
	if strings.TrimSpace(cfg.Bun) == "" {
		return nil, errors.New("providerruntime: Bun path is required")
	}
	if strings.TrimSpace(cfg.ConfigDir) == "" || strings.TrimSpace(cfg.AuthPath) == "" {
		return nil, errors.New("providerruntime: config and auth paths are required")
	}
	if err := atomicfile.EnsureDir(cfg.ConfigDir, 0o700); err != nil {
		return nil, err
	}
	hostPath := filepath.Join(cfg.ConfigDir, "provider-host.js")
	if err := atomicfile.WriteFile(hostPath, hostJS, 0o600); err != nil {
		return nil, err
	}
	directory := cfg.Directory
	if directory == "" {
		directory = cfg.ConfigDir
	}
	cmd := newCommand(cfg.Bun, "run", hostPath)
	cmd.Dir = directory
	if len(cfg.Env) > 0 {
		cmd.Env = append(os.Environ(), cfg.Env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	h := &Host{
		cmd: cmd, stdin: stdin, config: cfg, pending: map[int64]*hostCall{},
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go h.readLoop(stdout)
	go func() { _, _ = io.Copy(&h.stderr, stderr) }()
	go h.waitLoop()

	plugins := make([]map[string]any, 0, len(cfg.Plugins))
	for _, plugin := range cfg.Plugins {
		item := map[string]any{"spec": plugin.Spec, "target": plugin.Target}
		if len(plugin.Options) > 0 {
			item["options"] = plugin.Options
		}
		plugins = append(plugins, item)
	}
	params := map[string]any{
		"authPath":      cfg.AuthPath,
		"directory":     directory,
		"plugins":       plugins,
		"loadMillis":    cfg.LoadMillis,
		"listMillis":    cfg.ListMillis,
		"config":        map[string]any{},
		"prefer":        map[string]string{},
		"options":       map[string]any{},
		"modelsDevPath": "",
	}
	var initialized struct {
		Plugins []LoadedPlugin `json:"plugins"`
	}
	if err := h.call(ctx, "init", params, &initialized); err != nil {
		_ = h.Close()
		return nil, fmt.Errorf("providerruntime: init: %w", err)
	}
	h.loaded = append([]LoadedPlugin(nil), initialized.Plugins...)
	return h, nil
}

// LoadedPlugins returns the host's credential-free plugin load diagnostics.
func (h *Host) LoadedPlugins() []LoadedPlugin {
	return append([]LoadedPlugin(nil), h.loaded...)
}

// Close stops the host. Auth files are intentionally not deleted here because
// the owning service persists the final auth snapshot before closing.
func (h *Host) Close() error {
	if h == nil || !h.closed.CompareAndSwap(false, true) {
		return nil
	}
	_ = h.stdin.Close()
	if h.cmd.Process != nil {
		_ = h.cmd.Process.Kill()
	}
	h.mu.Lock()
	h.exited = true
	h.exitErr = ErrNotRunning
	for id, call := range h.pending {
		delete(h.pending, id)
		call.push(message{Error: &struct {
			Message string `json:"message"`
		}{Message: ErrNotRunning.Error()}})
	}
	h.mu.Unlock()
	return nil
}

func (h *Host) waitLoop() {
	err := h.cmd.Wait()
	h.mu.Lock()
	h.exited = true
	if h.closed.Load() {
		h.exitErr = ErrNotRunning
	} else if err != nil {
		h.exitErr = fmt.Errorf("providerruntime: host exited: %w", err)
	} else {
		h.exitErr = ErrNotRunning
	}
	for id, call := range h.pending {
		delete(h.pending, id)
		call.push(message{Error: &struct {
			Message string `json:"message"`
		}{Message: h.exitErr.Error()}})
	}
	h.mu.Unlock()
}

func (h *Host) readLoop(stdout io.Reader) {
	reader := bufio.NewReaderSize(stdout, 64<<10)
	for {
		line, err := readBoundedLine(reader, maxLineBytes)
		if len(line) > 0 {
			var m message
			if json.Unmarshal(line, &m) == nil {
				h.dispatch(m)
			}
		}
		if err != nil {
			return
		}
	}
}

func readBoundedLine(r *bufio.Reader, limit int) ([]byte, error) {
	var out []byte
	for {
		part, err := r.ReadSlice('\n')
		if len(out)+len(part) > limit {
			return nil, ErrProtocol
		}
		out = append(out, part...)
		if err == nil {
			return out, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return out, err
	}
}

func (h *Host) dispatch(m message) {
	if m.ID == 0 {
		return
	}
	h.mu.Lock()
	call := h.pending[m.ID]
	h.mu.Unlock()
	if call != nil {
		call.push(m)
	}
}

func (c *hostCall) push(m message) {
	if c.closed.Load() {
		return
	}
	// 缓冲已满时直接丢弃中间事件，绝不阻塞宿主：timeAfter 恒返回已关闭通道，
	// 原来的二次 select 实际等价于立即放弃，属于永远走不到的死分支，已简化为
	// 单次非阻塞投递。
	select {
	case c.ch <- m:
	default:
	}
}

func (h *Host) begin() (int64, *hostCall, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.exited {
		if h.exitErr != nil {
			return 0, nil, h.exitErr
		}
		return 0, nil, ErrNotRunning
	}
	h.nextID++
	call := &hostCall{ch: make(chan message, 256)}
	h.pending[h.nextID] = call
	return h.nextID, call, nil
}

func (h *Host) end(id int64, call *hostCall) {
	call.closed.Store(true)
	h.mu.Lock()
	delete(h.pending, id)
	h.mu.Unlock()
}

func (h *Host) send(id int64, method string, params any) error {
	line, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return err
	}
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	_, err = h.stdin.Write(append(line, '\n'))
	return err
}

func (h *Host) call(ctx context.Context, method string, params, out any) error {
	id, call, err := h.begin()
	if err != nil {
		return err
	}
	defer h.end(id, call)
	if err := h.send(id, method, params); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			_ = h.send(id, "abort", map[string]any{"id": id})
			return ctx.Err()
		case m := <-call.ch:
			if m.Event != "" {
				continue
			}
			if m.Error != nil {
				return errors.New(m.Error.Message)
			}
			if out == nil || len(m.Result) == 0 || string(m.Result) == "null" {
				return nil
			}
			return json.Unmarshal(m.Result, out)
		}
	}
}

// Providers lists all providers exposed by the local plugins.
func (h *Host) Providers(ctx context.Context) ([]Provider, error) {
	var providers []Provider
	if err := h.call(ctx, "providers", map[string]any{}, &providers); err != nil {
		return nil, err
	}
	return providers, nil
}

// Prompt returns the next sign-in question, if any.
func (h *Host) Prompt(ctx context.Context, provider string, method int, inputs map[string]string) (Prompt, error) {
	var r struct {
		Prompt *Prompt `json:"prompt"`
	}
	if err := h.call(ctx, "prompt", map[string]any{
		"provider": provider, "method": method, "inputs": inputs,
	}, &r); err != nil {
		return Prompt{}, err
	}
	if r.Prompt == nil {
		return Prompt{}, nil
	}
	return *r.Prompt, nil
}

// Authorize starts an OAuth sign-in.
func (h *Host) Authorize(ctx context.Context, provider string, method int, inputs map[string]string, account string) (Authorization, error) {
	var a Authorization
	err := h.call(ctx, "authorize", map[string]any{
		"provider": provider, "method": method, "inputs": inputs, "account": account,
	}, &a)
	return a, err
}

// Finish completes an OAuth sign-in.
func (h *Host) Finish(ctx context.Context, session, code string) (Saved, error) {
	var r struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		Saved
	}
	if err := h.call(ctx, "callback", map[string]any{
		"session": session, "code": code,
	}, &r); err != nil {
		return Saved{}, err
	}
	if !r.OK {
		return Saved{}, errors.New(firstNonEmpty(r.Error, "provider sign-in failed"))
	}
	return r.Saved, nil
}

// APIKey signs in with an API key.
func (h *Host) APIKey(ctx context.Context, provider string, method int, inputs map[string]string, key, account string) (Saved, error) {
	var r struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		Saved
	}
	if err := h.call(ctx, "apiKey", map[string]any{
		"provider": provider, "method": method, "inputs": inputs,
		"key": key, "account": account,
	}, &r); err != nil {
		return Saved{}, err
	}
	if !r.OK {
		return Saved{}, errors.New(firstNonEmpty(r.Error, "provider sign-in failed"))
	}
	return r.Saved, nil
}

// Usage reads one account's allowance.
func (h *Host) Usage(ctx context.Context, provider, account string) (Usage, error) {
	var usage Usage
	err := h.call(ctx, "usage", map[string]any{"provider": provider, "account": account}, &usage)
	return usage, err
}

// Checkin performs one account's daily check-in.
func (h *Host) Checkin(ctx context.Context, provider, account string) (Checkin, error) {
	var result Checkin
	err := h.call(ctx, "checkin", map[string]any{"provider": provider, "account": account}, &result)
	return result, err
}

// Load runs one account's auth loader.
func (h *Host) Load(ctx context.Context, provider, account string) (Loader, error) {
	var loader Loader
	err := h.call(ctx, "load", map[string]any{"provider": provider, "account": account}, &loader)
	return loader, err
}

// SignOut forgets one account in the host's auth view.
func (h *Host) SignOut(ctx context.Context, provider, account string) error {
	return h.call(ctx, "signOut", map[string]any{"provider": provider, "account": account}, nil)
}

// Fetch performs a signed provider request and collects its bounded body.
func (h *Host) Fetch(ctx context.Context, req FetchRequest) (FetchResponse, error) {
	id, call, err := h.begin()
	if err != nil {
		return FetchResponse{}, err
	}
	defer h.end(id, call)
	if req.Method == "" {
		req.Method = "POST"
	}
	if req.Window <= 0 {
		req.Window = 512 << 10
	}
	params := map[string]any{
		"provider": req.Provider, "account": req.Account, "url": req.URL,
		"method": req.Method, "headers": req.Headers,
		"body":   base64.StdEncoding.EncodeToString([]byte(req.Body)),
		"window": req.Window,
	}
	line, _ := json.Marshal(map[string]any{"id": id, "method": "fetch", "params": params})
	h.writeMu.Lock()
	_, err = h.stdin.Write(append(line, '\n'))
	h.writeMu.Unlock()
	if err != nil {
		return FetchResponse{}, err
	}

	result := FetchResponse{Headers: map[string]string{}}
	var body []byte
	for {
		select {
		case <-ctx.Done():
			_ = h.send(id, "abort", map[string]any{"id": id})
			return FetchResponse{}, ctx.Err()
		case m := <-call.ch:
			switch m.Event {
			case "head":
				result.Status = m.Status
				result.Headers = m.Headers
			case "chunk":
				decoded, err := base64.StdEncoding.DecodeString(m.Data)
				if err != nil {
					return FetchResponse{}, fmt.Errorf("%w: invalid chunk", ErrProtocol)
				}
				if len(body)+len(decoded) > 4<<20 {
					return FetchResponse{}, fmt.Errorf("%w: response body too large", ErrProtocol)
				}
				body = append(body, decoded...)
				_ = h.send(id, "credit", map[string]any{"id": id, "n": len(decoded)})
			default:
				if m.Error != nil {
					return FetchResponse{}, errors.New(m.Error.Message)
				}
			}
			if m.Event == "" && m.Error == nil {
				result.Body = body
				return result, nil
			}
		}
	}
}

// ReadAuth returns the temporary auth view.
func (h *Host) ReadAuth() ([]byte, error) {
	return os.ReadFile(h.config.AuthPath)
}

// RemoveAuth deletes the temporary auth view and all recovery sidecars that
// can contain a previous access or refresh token.
func (h *Host) RemoveAuth() error {
	return RemoveAuthFiles(h.config.AuthPath)
}

// RemoveAuthFiles removes the provider auth view plus host.js's backups,
// invalid-file copies, stale temporary writes, and lock file. The caller must
// only pass the QKA-owned temporary auth path.
func RemoveAuthFiles(authPath string) error {
	if strings.TrimSpace(authPath) == "" {
		return errors.New("providerruntime: auth path is required")
	}
	dir := filepath.Dir(authPath)
	base := filepath.Base(authPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var failures []error
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		managed := name == base || name == base+".bak" || name == base+".lock" ||
			strings.HasPrefix(name, base+".bad-") ||
			strings.HasPrefix(name, base+".tmp-")
		if !managed {
			continue
		}
		if err := removeAuthPath(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

type boundedBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	const limit = 32 << 10
	if len(b.buf) < limit {
		remain := limit - len(b.buf)
		if remain > len(p) {
			remain = len(p)
		}
		b.buf = append(b.buf, p[:remain]...)
	}
	return len(p), nil
}
