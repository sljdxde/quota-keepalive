package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"qka/internal/accounts"
	"qka/internal/engine"
	"qka/internal/logtail"
)

// newFakeDoubao 启动受控假目标：模拟 doubao-work 的发现与签到端点。
func newFakeDoubao(t *testing.T) *httptest.Server {
	return newFakeDoubaoWithHealth(t, "healthy")
}

func newFakeDoubaoWithHealth(t *testing.T, health string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/api/accounts", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accounts": []map[string]any{{
				"id":             "u1",
				"displayName":    "测试用户",
				"maskedIdentity": "****5678",
				"source":         map[string]any{"id": "src1", "kind": "managed", "health": health},
			}},
		})
	})

	mux.HandleFunc("/api/checkin", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":        "ok",
			"balanceBefore": 100,
			"balanceAfter":  300,
			"receipt":       "rcpt-001",
		})
	})

	return httptest.NewServer(mux)
}

func rawJSON(s string) json.RawMessage { return json.RawMessage(s) }

func buildTestApp(t *testing.T, fakeURL string) *App {
	t.Helper()
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("未找到 bun，跳过端到端测试")
	}
	adaptersRoot, err := filepath.Abs("../../adapters")
	if err != nil {
		t.Fatal(err)
	}

	a, err := Build(context.Background(), Config{
		ConfigDir:    t.TempDir(),
		AdaptersRoot: adaptersRoot,
		BunPath:      bun,
		Version:      "v0.0.1",
		BaseURLs:     map[string]string{"doubao-work": fakeURL},
	})
	if err != nil {
		t.Fatalf("Build 失败: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestDiscoverAndCheckinEndToEnd(t *testing.T) {
	ctx := context.Background()
	fake := newFakeDoubao(t)
	defer fake.Close()
	a := buildTestApp(t, fake.URL)

	// 1) 发现账号并落库。
	disc, err := a.CallTool(ctx, "accounts_manage",
		rawJSON(`{"action":"discover","target":"doubao-work"}`))
	if err != nil {
		t.Fatalf("discover 失败: %v", err)
	}
	accts, ok := disc.([]accounts.Account)
	if !ok || len(accts) != 1 || accts[0].ID != "u1" {
		t.Fatalf("发现账号结果异常: %#v", disc)
	}

	// 2) 执行签到：状态机推进到 provisional/confirmed，且确实发起了请求。
	obs, err := a.CallTool(ctx, "checkin_run",
		rawJSON(`{"target":"doubao-work","account_id":"u1"}`))
	if err != nil {
		t.Fatalf("checkin 失败: %v", err)
	}
	engineObs, ok := obs.(engine.Observation)
	if !ok {
		t.Fatalf("checkin 结果类型异常: %#v", obs)
	}
	if !engineObs.Executed {
		t.Errorf("签到应真实发起请求，Executed=false")
	}
	if engineObs.State != engine.SignedProvisional && engineObs.State != engine.SignedConfirmed {
		t.Errorf("签到状态应为 provisional/confirmed，实际 %s", engineObs.State)
	}

	// 3) 日志：适配器经 log.write 写入「签到完成」。
	page, err := a.CallTool(ctx, "log_tail", rawJSON(`{"target":"doubao-work"}`))
	if err != nil {
		t.Fatalf("log_tail 失败: %v", err)
	}
	if len(page.(*logtail.Page).Entries) == 0 {
		t.Errorf("应至少有一条签到日志，实际为空")
	}

	// 4) status 汇总。
	if _, err := a.CallTool(ctx, "status", nil); err != nil {
		t.Fatalf("status 失败: %v", err)
	}

	// 5) doctor 与 feed 不报错。
	if _, err := a.CallTool(ctx, "doctor_run", nil); err != nil {
		t.Fatalf("doctor_run 失败: %v", err)
	}
	if _, err := a.CallTool(ctx, "feed_list", nil); err != nil {
		t.Fatalf("feed_list 失败: %v", err)
	}
}

func TestDiscoverCarriesSourceHealth(t *testing.T) {
	fake := newFakeDoubaoWithHealth(t, "reauth_required")
	defer fake.Close()
	a := buildTestApp(t, fake.URL)

	found, err := a.Discover(context.Background(), "doubao-work")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Health != accounts.HealthReauthRequired {
		t.Fatalf("发现结果应携带来源健康状态，实际 %+v", found)
	}
}

func TestAccountsManageRejectsEnableWithoutHealthySource(t *testing.T) {
	fake := newFakeDoubaoWithHealth(t, "reauth_required")
	defer fake.Close()
	a := buildTestApp(t, fake.URL)
	ctx := context.Background()

	if _, err := a.CallTool(ctx, "accounts_manage",
		rawJSON(`{"action":"discover","target":"doubao-work"}`)); err != nil {
		t.Fatal(err)
	}
	_, err := a.CallTool(ctx, "accounts_manage",
		rawJSON(`{"action":"set_enabled","target":"doubao-work","account_id":"u1","enabled":true}`))
	if err == nil || !strings.Contains(err.Error(), "无可用健康来源") {
		t.Fatalf("无健康来源时应返回可操作失败，实际 %v", err)
	}

	list, err := a.CallTool(ctx, "accounts_manage",
		rawJSON(`{"action":"list","target":"doubao-work"}`))
	if err != nil {
		t.Fatal(err)
	}
	accts := list.([]accounts.Account)
	if len(accts) != 1 || accts[0].Enabled {
		t.Fatalf("启用失败后账号必须保持停用，实际 %+v", accts)
	}
}

func TestServeOverHTTPToolsCallExecutes(t *testing.T) {
	ctx := context.Background()
	fake := newFakeDoubao(t)
	defer fake.Close()
	a := buildTestApp(t, fake.URL)

	httpSrv, err := a.Serve(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Serve 启动失败: %v", err)
	}
	defer func() { _ = httpSrv.Shutdown(ctx) }()

	// initialize
	initResp := postAppRPC(t, httpSrv, "initialize", nil)
	if initResp.Error != nil {
		t.Fatalf("initialize 报错: %+v", initResp.Error)
	}

	// tools/list 恰好 8 个。
	listResp := postAppRPC(t, httpSrv, "tools/list", nil)
	var listResult struct {
		Tools []struct{ Name string } `json:"tools"`
	}
	if err := json.Unmarshal(listResp.Result, &listResult); err != nil {
		t.Fatalf("tools/list 解析失败: %v", err)
	}
	if len(listResult.Tools) != 8 {
		t.Fatalf("工具数量应为 8，实际 %d", len(listResult.Tools))
	}

	// tools/call log_tail：应返回真实结果（MCP text content），不再是 not-implemented。
	args, _ := json.Marshal(map[string]any{"name": "log_tail", "arguments": map[string]any{"limit": 5}})
	callResp := postAppRPC(t, httpSrv, "tools/call", args)
	if callResp.Error != nil {
		t.Fatalf("tools/call 报错: %+v", callResp.Error)
	}
	var callResult struct {
		Content []struct{ Type, Text string } `json:"content"`
	}
	if err := json.Unmarshal(callResp.Result, &callResult); err != nil {
		t.Fatalf("tools/call 结果解析失败: %v", err)
	}
	if len(callResult.Content) == 0 || callResult.Content[0].Text == "" {
		t.Fatalf("tools/call 应返回非空文本内容，实际 %s", string(callResp.Result))
	}
}

type appRPCResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func postAppRPC(t *testing.T, srv *http.Server, method string, params json.RawMessage) appRPCResponse {
	t.Helper()
	req := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
	if params != nil {
		req["params"] = json.RawMessage(params)
	}
	body, _ := json.Marshal(req)

	resp, err := http.Post("http://"+srv.Addr+"/mcp", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("HTTP 调用失败: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	var out appRPCResponse
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("响应解析失败: %v\n%s", err, data)
	}
	return out
}
