package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := NewServer(ServerDeps{})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func postRPC(t *testing.T, ts *httptest.Server, method string, params json.RawMessage) rpcResponse {
	t.Helper()
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  method,
		Params:  params,
	}
	data, _ := json.Marshal(req)
	resp, err := http.Post(ts.URL+"/mcp", "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("POST 失败: %v", err)
	}
	defer resp.Body.Close()

	var out rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("响应解析失败: %v", err)
	}
	return out
}

func TestInitializeHandshake(t *testing.T) {
	ts := newTestServer(t)
	out := postRPC(t, ts, "initialize", nil)
	if out.Error != nil {
		t.Fatalf("initialize 报错: %+v", out.Error)
	}

	var result initializeResult
	if err := json.Unmarshal(out.Result, &result); err != nil {
		t.Fatalf("initialize 结果解析失败: %v", err)
	}
	if result.ProtocolVersion != protocolVersion {
		t.Errorf("协议版本错误: %s", result.ProtocolVersion)
	}
	if result.ServerInfo.Name != "QKA" {
		t.Errorf("serverInfo 错误: %+v", result.ServerInfo)
	}
}

func TestToolsListReturnsExactlyEight(t *testing.T) {
	ts := newTestServer(t)
	out := postRPC(t, ts, "tools/list", nil)
	if out.Error != nil {
		t.Fatalf("tools/list 报错: %+v", out.Error)
	}

	var result toolsListResult
	if err := json.Unmarshal(out.Result, &result); err != nil {
		t.Fatalf("tools/list 结果解析失败: %v", err)
	}

	want := []string{
		"agents_list",
		"accounts_manage",
		"checkin_run",
		"status",
		"plan_validate",
		"log_tail",
		"doctor_run",
		"feed_list",
	}
	if len(result.Tools) != len(want) {
		t.Fatalf("工具数量错误: 期望 %d，实际 %d", len(want), len(result.Tools))
	}
	for i, name := range want {
		if result.Tools[i].Name != name {
			t.Errorf("位置 %d 期望 %s，实际 %s", i, name, result.Tools[i].Name)
		}
	}
}

func TestToolsListContainsNoScheduleOrFallback(t *testing.T) {
	ts := newTestServer(t)
	out := postRPC(t, ts, "tools/list", nil)

	var result toolsListResult
	_ = json.Unmarshal(out.Result, &result)
	for _, tool := range result.Tools {
		name := tool.Name
		if name == "schedule_get" || name == "schedule_set" ||
			bytes.Contains([]byte(name), []byte("schedule")) ||
			bytes.Contains([]byte(name), []byte("fallback")) {
			t.Errorf("工具面不得含 schedule/fallback，发现 %s", name)
		}
	}
}

func TestUnimplementedToolReturnsExplicitError(t *testing.T) {
	ts := newTestServer(t)
	params, _ := json.Marshal(map[string]any{"name": "checkin_run", "arguments": map[string]any{}})
	out := postRPC(t, ts, "tools/call", params)

	if out.Error == nil {
		t.Fatal("未实现工具应返回显式错误，实际成功")
	}
	if out.Error.Code != errToolNotImplemented {
		t.Errorf("错误码应稳定为 %d，实际 %d", errToolNotImplemented, out.Error.Code)
	}
}

func TestUnknownMethodReturnsMethodNotFound(t *testing.T) {
	ts := newTestServer(t)
	out := postRPC(t, ts, "nonexistent/method", nil)
	if out.Error == nil || out.Error.Code != errMethodNotFound {
		t.Fatalf("未知方法应返回 method-not-found，实际 %+v", out.Error)
	}
}

func TestServerBindsLoopback(t *testing.T) {
	srv := NewServer(ServerDeps{})
	httpSrv, err := srv.ListenAndServe("127.0.0.1:0")
	if err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	defer func() { _ = httpSrv.Shutdown(context.Background()) }()

	addr := httpSrv.Addr
	if !bytes.HasPrefix([]byte(addr), []byte("127.0.0.1:")) {
		t.Errorf("服务应仅绑定 loopback，实际 %s", addr)
	}
}
