package providers

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"qka/internal/providerruntime"
)

// TestAuditStateLabelMapping 表驱动覆盖日志状态到中文标签的全部映射。
func TestAuditStateLabelMapping(t *testing.T) {
	cases := map[string]string{
		"":                     "",
		"signed_confirmed":     "已确认到账",
		"signed_provisional":   "已领取，待确认",
		"already":              "今日已领取",
		"manual_required":      "需要人工处理",
		"failed_retryable":     "网络或上游暂时失败",
		"failed_final":         "执行失败",
		"completed":            "已完成",
		"ineligible":           "ineligible",
		"inactive":             "inactive",
		"unknown-future-state": "unknown-future-state",
	}
	for state, want := range cases {
		if got := auditStateLabel(state); got != want {
			t.Errorf("auditStateLabel(%q) = %q, want %q", state, got, want)
		}
	}
}

// TestAuditOperationLabelMapping 表驱动覆盖操作类型标签与未知/空操作兜底。
func TestAuditOperationLabelMapping(t *testing.T) {
	cases := map[string]string{
		"checkin":    "签到",
		"refresh":    "额度刷新",
		"keepalive":  "保活",
		"model_test": "模型测试",
		"login":      "登录",
		"account":    "账号设置",
		"provider":   "供应商设置",
		"automation": "自动执行",
		"future-op":  "future-op",
		"":           "操作",
	}
	for operation, want := range cases {
		if got := auditOperationLabel(operation); got != want {
			t.Errorf("auditOperationLabel(%q) = %q, want %q", operation, got, want)
		}
	}
}

// TestModelTestStatusMessageAndFailureState 覆盖模型测试状态文案与可重试判定。
func TestModelTestStatusMessageAndFailureState(t *testing.T) {
	messages := map[int]string{
		400: "请求被供应商拒绝，请检查模型参数",
		401: "鉴权失败，请重新登录该账号",
		403: "鉴权失败，请重新登录该账号",
		404: "模型或接口不存在",
		408: "供应商响应超时",
		429: "请求被限流，请稍后再试",
		500: "供应商服务暂时异常",
		503: "供应商服务暂时异常",
		418: "模型测试返回 HTTP 418",
	}
	for status, want := range messages {
		if got := modelTestStatusMessage(status); got != want {
			t.Errorf("modelTestStatusMessage(%d) = %q, want %q", status, got, want)
		}
	}
	retryable := map[int]bool{
		0: true, 408: true, 429: true, 500: true, 503: true,
		400: false, 401: false, 403: false, 404: false,
	}
	for status, want := range retryable {
		wantState := "failed_final"
		if want {
			wantState = "failed_retryable"
		}
		if got := modelTestFailureState(status); got != wantState {
			t.Errorf("modelTestFailureState(%d) = %q, want %q", status, got, wantState)
		}
	}
}

// TestModelTestTargetSelection 覆盖模型/接口地址选择的全部分支：
// 模型缺失、账号白名单、候选地址优先级与 anthropic/OpenAI 协议后缀。
func TestModelTestTargetSelection(t *testing.T) {
	base := providerruntime.Provider{
		ID: "p", NPM: "@ai-sdk/openai-compatible",
		APIURL: "https://provider.example/v1",
		Models: []providerruntime.Model{
			{ID: "model-a", NPM: "@ai-sdk/openai-compatible"},
			{ID: "model-anthropic", NPM: "@ai-sdk/anthropic"},
			{ID: "model-custom", APIURL: "https://custom.example/api/"},
			{ID: "model-no-url"},
		},
		Accounts: []providerruntime.Account{{Key: "a1"}},
	}

	if _, endpoint, err := modelTestTarget(base, "a1", "model-a", ""); err != nil ||
		endpoint != "https://provider.example/v1/chat/completions" {
		t.Fatalf("openai endpoint = %q, %v", endpoint, err)
	}
	if _, endpoint, err := modelTestTarget(base, "a1", "model-anthropic", ""); err != nil ||
		endpoint != "https://provider.example/v1/messages" {
		t.Fatalf("anthropic endpoint = %q, %v", endpoint, err)
	}
	if _, endpoint, err := modelTestTarget(base, "a1", "model-custom", ""); err != nil ||
		endpoint != "https://custom.example/api/chat/completions" {
		t.Fatalf("custom endpoint = %q, %v", endpoint, err)
	}
	// 已经带协议后缀时不得重复拼接。
	withSuffix := base
	withSuffix.APIURL = "https://provider.example/v1/chat/completions"
	if _, endpoint, err := modelTestTarget(withSuffix, "a1", "model-a", ""); err != nil ||
		endpoint != withSuffix.APIURL {
		t.Fatalf("suffixed endpoint = %q, %v", endpoint, err)
	}

	noURL := base
	noURL.APIURL = ""
	noURL.Models = []providerruntime.Model{{ID: "model-no-url"}}
	if _, _, err := modelTestTarget(noURL, "a1", "model-no-url", ""); err == nil ||
		!strings.Contains(err.Error(), "没有可用的接口地址") {
		t.Fatalf("no URL error = %v", err)
	}

	if _, _, err := modelTestTarget(base, "a1", "missing-model", ""); err == nil ||
		!strings.Contains(err.Error(), "没有此模型") {
		t.Fatalf("missing model error = %v", err)
	}

	restricted := base
	restricted.Accounts = []providerruntime.Account{{Key: "a1", Models: []string{"model-a"}}}
	if _, _, err := modelTestTarget(restricted, "a1", "model-anthropic", ""); err == nil ||
		!strings.Contains(err.Error(), "不能使用该模型") {
		t.Fatalf("restricted model error = %v", err)
	}
	if _, endpoint, err := modelTestTarget(restricted, "a1", "model-a", ""); err != nil ||
		endpoint == "" {
		t.Fatalf("allowed model endpoint = %q, %v", endpoint, err)
	}

	// fallbackBaseURL 仅在模型与 provider 都没有地址时兜底。
	fallbackOnly := providerruntime.Provider{
		ID: "p", NPM: "@ai-sdk/openai-compatible",
		Models:   []providerruntime.Model{{ID: "model-a"}},
		Accounts: []providerruntime.Account{{Key: "a1"}},
	}
	if _, endpoint, err := modelTestTarget(fallbackOnly, "a1", "model-a", "https://fallback.example/v1"); err != nil ||
		endpoint != "https://fallback.example/v1/chat/completions" {
		t.Fatalf("fallback endpoint = %q, %v", endpoint, err)
	}
}

// TestSetAccountEnabledTogglesAndFailures 覆盖账号启停的正常路径、重复设置幂等
// 与未知账号错误路径（错误必须回传且写入审计失败日志）。
func TestSetAccountEnabledTogglesAndFailures(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.service.List(ctx); err != nil {
		t.Fatal(err)
	}

	account, err := f.account.Get(ctx, "fixture", "a1")
	if err != nil {
		t.Fatal(err)
	}
	if !account.Enabled {
		t.Fatal("新发现账号默认应启用")
	}

	for _, enabled := range []bool{false, false, true, true} {
		if err := f.service.SetAccountEnabled(ctx, "fixture", "a1", enabled); err != nil {
			t.Fatalf("SetAccountEnabled(%v): %v", enabled, err)
		}
		got, err := f.account.Get(ctx, "fixture", "a1")
		if err != nil {
			t.Fatal(err)
		}
		if got.Enabled != enabled {
			t.Fatalf("账号启用状态 = %v, want %v", got.Enabled, enabled)
		}
	}

	if err := f.service.SetAccountEnabled(ctx, "fixture", "missing", true); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("未知账号应返回未找到，实际 %v", err)
	}
	var logs int
	if err := f.store.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM logs WHERE operation = ? AND state = 'failed_final'`,
		"account").Scan(&logs); err != nil {
		t.Fatal(err)
	}
	if logs == 0 {
		t.Fatal("账号设置失败应写入审计日志")
	}
}

// TestSetTaskEnabledTogglesAndFailures 覆盖自动任务开关的写入、幂等、空操作与
// 未知账号错误路径。
func TestSetTaskEnabledTogglesAndFailures(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.service.List(ctx); err != nil {
		t.Fatal(err)
	}

	for _, enabled := range []bool{false, false, true} {
		if err := f.service.SetTaskEnabled(ctx, "fixture", "a1", "checkin", enabled); err != nil {
			t.Fatalf("SetTaskEnabled(%v): %v", enabled, err)
		}
		prefs, err := f.account.TaskPreferences(ctx, "fixture", "a1")
		if err != nil {
			t.Fatal(err)
		}
		if prefs["checkin"] != enabled {
			t.Fatalf("自动任务偏好 = %v, want %v", prefs["checkin"], enabled)
		}
	}

	if err := f.service.SetTaskEnabled(ctx, "fixture", "a1", "", true); err == nil ||
		!strings.Contains(err.Error(), "操作类型不能为空") {
		t.Fatalf("空操作应被拒绝，实际 %v", err)
	}
	if err := f.service.SetTaskEnabled(ctx, "fixture", "missing", "checkin", true); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("未知账号应返回未找到，实际 %v", err)
	}
	var logs int
	if err := f.store.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM logs WHERE operation = ? AND state = 'failed_final'`,
		"automation").Scan(&logs); err != nil {
		t.Fatal(err)
	}
	if logs == 0 {
		t.Fatal("自动任务设置失败应写入审计日志")
	}
}
