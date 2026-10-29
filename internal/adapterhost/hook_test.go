package adapterhost

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

type recordingHookHandler struct {
	method string
	params json.RawMessage
	result json.RawMessage
	err    error
}

func (r *recordingHookHandler) HandleHook(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	r.method = method
	r.params = params
	return r.result, r.err
}

func TestAdapterCanCallHostHook(t *testing.T) {
	dir := filepath.Join("testdata", "hook-adapter")
	hooks := &recordingHookHandler{result: json.RawMessage(`{"echoed":"hello"}`)}

	h, err := LaunchWith(context.Background(), "bun", dir, hooks)
	if err != nil {
		t.Fatalf("LaunchWith 失败: %v", err)
	}
	defer h.Close()

	result, err := h.Call(context.Background(), "useHook")
	if err != nil {
		t.Fatalf("Call 失败: %v", err)
	}
	if hooks.method != "echo" {
		t.Errorf("钩子方法应转发为 echo，实际 %q", hooks.method)
	}
	if !json.Valid(result) {
		t.Errorf("钩子结果应是合法 JSON，实际 %s", result)
	}
	var got map[string]string
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatal(err)
	}
	if got["echoed"] != "hello" {
		t.Errorf("钩子结果应回传给适配器，实际 %s", result)
	}
}

func TestAdapterHookErrorReturned(t *testing.T) {
	dir := filepath.Join("testdata", "hook-adapter")
	hooks := &recordingHookHandler{
		err: errors.New("hook denied"),
	}

	h, err := LaunchWith(context.Background(), "bun", dir, hooks)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()

	_, err = h.Call(context.Background(), "useHook")
	if err == nil {
		t.Fatal("钩子返回错误时，action 应失败")
	}
}
