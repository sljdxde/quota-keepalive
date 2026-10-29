package adapterhost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

const (
	// MaxMessageBytes 限制单条适配器消息大小，超限显式失败（NDJSON 单行）。
	MaxMessageBytes = 1 << 20 // 1 MiB
	// MaxStderrBytes 限制收集的 stderr 量，超出部分丢弃但仍消费，避免阻塞子进程。
	MaxStderrBytes = 16 << 10
)

var (
	// ErrUndeclaredAction：未在 manifest actions 中声明的操作拒绝派发。
	ErrUndeclaredAction = errors.New("操作未在 manifest actions 中声明，拒绝派发")
	// ErrAdapterCrashed：适配器子进程已崩溃退出。
	ErrAdapterCrashed = errors.New("适配器子进程已退出")
	// ErrMessageTooLarge：适配器输出超过有界上限。
	ErrMessageTooLarge = errors.New("适配器消息超过有界上限")
	errHostClosed      = errors.New("适配器宿主已关闭")
)

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("适配器 rpc 错误 %d: %s", e.Code, e.Message)
}

// Host 是一个适配器子进程的生命周期与 JSON-RPC 通道。
type Host struct {
	manifest Manifest
	cmd      *exec.Cmd
	stdin    io.WriteCloser

	shutdownOnce sync.Once
	mu           sync.Mutex
	nextID       int
	pending      map[int]chan rpcResponse
	exited       bool
	exitErr      error
	stderr       stderrCollector
}

// Launch 启动适配器子进程并完成 initialize 握手。
func Launch(ctx context.Context, bunPath, adapterDir string) (*Host, error) {
	manifest, err := LoadManifest(adapterDir)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(bunPath, "run", manifest.Entry)
	cmd.Dir = adapterDir

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
		manifest: manifest,
		cmd:      cmd,
		stdin:    stdin,
		pending:  map[int]chan rpcResponse{},
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go h.readLoop(stdout)
	go h.drainStderr(stderr)
	go h.waitLoop()

	if _, err := h.request(ctx, "initialize", map[string]any{"protocol": "qka-adapter"}); err != nil {
		_ = h.Close()
		return nil, fmt.Errorf("适配器 initialize 握手失败: %w", err)
	}
	return h, nil
}

func (h *Host) waitLoop() {
	err := h.cmd.Wait()
	h.shutdown(err, false)
}

func (h *Host) readLoop(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), MaxMessageBytes)
	for scanner.Scan() {
		var resp rpcResponse
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			continue // 非协议行忽略
		}
		h.deliver(resp)
	}
	if err := scanner.Err(); err != nil {
		// 单行超过上限：显式失败并终止已不可信的子进程。
		h.shutdown(ErrMessageTooLarge, true)
	}
}

func (h *Host) drainStderr(stderr io.Reader) {
	_, _ = io.Copy(&h.stderr, stderr)
}

func (h *Host) deliver(resp rpcResponse) {
	h.mu.Lock()
	ch, ok := h.pending[resp.ID]
	if ok {
		delete(h.pending, resp.ID)
	}
	h.mu.Unlock()
	if ok {
		ch <- resp
	}
}

// shutdown 是进程退出或协议致命错误的唯一收口（幂等）。
func (h *Host) shutdown(cause error, killProcess bool) {
	h.shutdownOnce.Do(func() {
		if killProcess && h.cmd.Process != nil {
			_ = h.cmd.Process.Kill()
		}

		h.mu.Lock()
		h.exited = true
		h.exitErr = normalizeExitCause(cause)
		pending := h.pending
		h.pending = map[int]chan rpcResponse{}
		h.mu.Unlock()

		_ = h.stdin.Close()

		failed := rpcResponse{Error: toRPCError(h.exitErr)}
		for _, ch := range pending {
			ch <- failed
		}
	})
}

func toRPCError(cause error) *rpcError {
	if cause == nil {
		cause = ErrAdapterCrashed
	}
	return &rpcError{Code: -32000, Message: cause.Error()}
}

// normalizeExitCause 把退出原因收敛为可被 errors.Is 识别的权威错误。
func normalizeExitCause(cause error) error {
	switch {
	case errors.Is(cause, ErrMessageTooLarge):
		return ErrMessageTooLarge
	case errors.Is(cause, errHostClosed):
		return errHostClosed
	case cause == nil:
		return ErrAdapterCrashed
	default:
		return fmt.Errorf("%w: %v", ErrAdapterCrashed, cause)
	}
}

func (h *Host) request(ctx context.Context, method string, params any) (rpcResponse, error) {
	h.mu.Lock()
	if h.exited {
		cause := h.exitErr
		h.mu.Unlock()
		return rpcResponse{}, cause
	}
	h.nextID++
	id := h.nextID
	ch := make(chan rpcResponse, 1)
	h.pending[id] = ch
	h.mu.Unlock()

	line, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return rpcResponse{}, err
	}
	if _, err := h.stdin.Write(append(line, '\n')); err != nil {
		h.removePending(id)
		return rpcResponse{}, err
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			// 本地 shutdown 合成的致命错误：返回权威 cause 以保留 errors.Is 链。
			h.mu.Lock()
			cause := h.exitErr
			exited := h.exited
			h.mu.Unlock()
			if exited && cause != nil {
				return resp, cause
			}
			return resp, resp.Error
		}
		return resp, nil
	case <-ctx.Done():
		h.removePending(id)
		return rpcResponse{}, ctx.Err()
	}
}

func (h *Host) removePending(id int) {
	h.mu.Lock()
	delete(h.pending, id)
	h.mu.Unlock()
}

// Call 派发一个已在 manifest 中声明的动作；未声明的 op 直接拒绝，不发给子进程。
func (h *Host) Call(ctx context.Context, op string) (json.RawMessage, error) {
	if !h.manifest.HasAction(op) {
		return nil, fmt.Errorf("%w: %q", ErrUndeclaredAction, op)
	}
	resp, err := h.request(ctx, "action", map[string]any{"op": op})
	if err != nil {
		return nil, err
	}
	return resp.Result, nil
}

// Exited 报告子进程是否已崩溃/退出。
func (h *Host) Exited() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.exited
}

// Manifest 返回已加载并校验的清单。
func (h *Host) Manifest() Manifest {
	return h.manifest
}

// Stderr 返回有界收集的 stderr（用于诊断）。
func (h *Host) Stderr() string {
	return h.stderr.String()
}

// Close 终止子进程并释放资源（崩溃隔离：不影响宿主进程）。
func (h *Host) Close() error {
	h.shutdown(errHostClosed, true)
	return nil
}

// stderrCollector 有界收集 stderr，超出上限后丢弃但仍消费，避免子进程写阻塞。
type stderrCollector struct {
	mu    sync.Mutex
	data  []byte
	limit int
}

func (s *stderrCollector) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.limit == 0 {
		s.limit = MaxStderrBytes
	}
	if len(s.data) < s.limit {
		room := s.limit - len(s.data)
		if room > len(p) {
			room = len(p)
		}
		s.data = append(s.data, p[:room]...)
	}
	return len(p), nil
}

func (s *stderrCollector) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.data)
}
