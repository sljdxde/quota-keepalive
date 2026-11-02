package providers

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"qka/internal/engine"
	"qka/internal/providerruntime"
	"qka/internal/requestguard"
)

// TestModelTestEdgeCases 覆盖 testModel 的参数校验、运行时错误、账号选择、
// Load/目标解析失败与协议差异等分支。
func TestModelTestEdgeCases(t *testing.T) {
	ctx := context.Background()

	t.Run("空模型", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.service.TestModel(ctx, "fixture", "a1", "  "); err == nil ||
			!strings.Contains(err.Error(), "请选择要测试的模型") {
			t.Fatalf("空模型应被拒绝，实际 %v", err)
		}
	})
	t.Run("停用账号仍可诊断测试", func(t *testing.T) {
		// 模型测试是只读诊断：用户停用供应商/账号后仍然需要验证登录态与
		// 模型连通性，所以这里不能再按「已停用」拒绝。
		f := newFixture(t)
		if _, err := f.service.List(ctx); err != nil {
			t.Fatal(err)
		}
		if err := f.service.SetEnabled(ctx, "fixture", true); err != nil {
			t.Fatal(err)
		}
		if err := f.service.SetAccountEnabled(ctx, "fixture", "a1", false); err != nil {
			t.Fatal(err)
		}
		view, err := f.service.TestModel(ctx, "fixture", "a1", "model-a")
		if err != nil {
			t.Fatalf("停用账号的诊断测试不应被拒绝，实际 %v", err)
		}
		if !view.OK {
			t.Fatalf("停用账号的诊断测试应返回可用: %+v", view)
		}
	})
	t.Run("未知供应商", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.service.TestModel(ctx, "unknown", "a1", "model-a"); err == nil {
			t.Fatal("未知供应商应失败")
		}
	})
	t.Run("自动选择首个账号", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.service.List(ctx); err != nil {
			t.Fatal(err)
		}
		if err := f.service.SetEnabled(ctx, "fixture", true); err != nil {
			t.Fatal(err)
		}
		view, err := f.service.TestModel(ctx, "fixture", "", "model-a")
		if err != nil || !view.OK || view.Model != "model-a" {
			t.Fatalf("自动选择账号失败: %+v %v", view, err)
		}
	})
	t.Run("没有账号", func(t *testing.T) {
		f := newFixture(t)
		f.runtime.providers[0].Accounts = nil
		if _, err := f.service.TestModel(ctx, "fixture", "", "model-a"); err == nil ||
			!strings.Contains(err.Error(), "尚未添加账号") {
			t.Fatalf("无账号应被拒绝，实际 %v", err)
		}
	})
	t.Run("账号不存在", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.service.TestModel(ctx, "fixture", "nope", "model-a"); err == nil ||
			!strings.Contains(err.Error(), "账号不存在") {
			t.Fatalf("不存在账号应被拒绝，实际 %v", err)
		}
	})
	t.Run("读取登录态失败", func(t *testing.T) {
		f := newFixture(t)
		f.runtime.loadErr = errors.New("load failed")
		if _, err := f.service.TestModel(ctx, "fixture", "a1", "model-a"); err == nil ||
			!strings.Contains(err.Error(), "读取供应商登录态失败") {
			t.Fatalf("Load 错误应回传，实际 %v", err)
		}
	})
	t.Run("模型不存在", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.service.TestModel(ctx, "fixture", "a1", "model-nope"); err == nil ||
			!strings.Contains(err.Error(), "没有此模型") {
			t.Fatalf("未知模型应被拒绝，实际 %v", err)
		}
	})
	t.Run("anthropic 协议", func(t *testing.T) {
		f := newFixture(t)
		f.runtime.providers[0].Models = append(f.runtime.providers[0].Models,
			providerruntime.Model{ID: "model-anthropic", NPM: "@ai-sdk/anthropic"})
		view, err := f.service.TestModel(ctx, "fixture", "a1", "model-anthropic")
		if err != nil || !view.OK {
			t.Fatalf("anthropic 模型测试失败: %+v %v", view, err)
		}
		if !strings.Contains(f.runtime.fetchRequest.Body, "system") {
			t.Fatalf("anthropic 请求体缺少 system: %s", f.runtime.fetchRequest.Body)
		}
	})
	t.Run("流式重试", func(t *testing.T) {
		f := newFixture(t)
		f.runtime.fetchQueue = []providerruntime.FetchResponse{
			{Status: 400, Body: []byte(`{"error":"non-stream request rejected"}`)},
			{Status: 200, Body: []byte(`{"choices":[{"message":{"content":"OK"}}]}`)},
		}
		view, err := f.service.TestModel(ctx, "fixture", "a1", "model-a")
		if err != nil || !view.OK || f.runtime.fetchCalls != 2 {
			t.Fatalf("流式重试失败: %+v %v fetch=%d", view, err, f.runtime.fetchCalls)
		}
		if !strings.Contains(f.runtime.fetchRequest.Body, `"stream":true`) {
			t.Fatalf("重试请求应为流式: %s", f.runtime.fetchRequest.Body)
		}
	})
	t.Run("网络失败", func(t *testing.T) {
		f := newFixture(t)
		f.runtime.fetchErr = errors.New("network down")
		view, err := f.service.TestModel(ctx, "fixture", "a1", "model-a")
		if err != nil || view.OK || !strings.Contains(view.Message, "模型请求失败") {
			t.Fatalf("网络失败视图错误: %+v %v", view, err)
		}
	})
	t.Run("成功但无回复", func(t *testing.T) {
		f := newFixture(t)
		f.runtime.fetch = providerruntime.FetchResponse{Status: 200, Body: []byte(`{}`)}
		view, err := f.service.TestModel(ctx, "fixture", "a1", "model-a")
		if err != nil || view.OK || !strings.Contains(view.Message, "没有模型回复") {
			t.Fatalf("无回复视图错误: %+v %v", view, err)
		}
	})
	t.Run("限流状态", func(t *testing.T) {
		f := newFixture(t)
		f.runtime.fetch = providerruntime.FetchResponse{Status: 429, Body: []byte(`{}`)}
		view, err := f.service.TestModel(ctx, "fixture", "a1", "model-a")
		if err != nil || view.OK || view.Status != 429 ||
			!strings.Contains(view.Message, "限流") {
			t.Fatalf("限流视图错误: %+v %v", view, err)
		}
	})
}

// TestModelTestRuntimeFactoryError 覆盖 provider runtime 启动失败。
func TestModelTestRuntimeFactoryError(t *testing.T) {
	f := newFixture(t)
	f.service.factory = func(context.Context) (Runtime, error) {
		return nil, errors.New("runtime unavailable")
	}
	if _, err := f.service.TestModel(context.Background(), "fixture", "a1", "model-a"); err == nil ||
		!strings.Contains(err.Error(), "runtime unavailable") {
		t.Fatalf("runtime 启动失败应回传，实际 %v", err)
	}
}

// TestModelTestTargetSkipsOtherAccounts 覆盖账号白名单遍历时跳过其他账号。
func TestModelTestTargetSkipsOtherAccounts(t *testing.T) {
	provider := providerruntime.Provider{
		ID: "p", NPM: "@ai-sdk/openai-compatible",
		APIURL:   "https://provider.example/v1",
		Models:   []providerruntime.Model{{ID: "model-a"}},
		Accounts: []providerruntime.Account{{Key: "other"}, {Key: "a1"}},
	}
	if _, endpoint, err := modelTestTarget(provider, "a1", "model-a", ""); err != nil ||
		endpoint == "" {
		t.Fatalf("目标账号匹配失败: %q %v", endpoint, err)
	}
}

// TestModelViewNameFallsBackToID 覆盖列表投影里模型名为空时回退 ID。
func TestModelViewNameFallsBackToID(t *testing.T) {
	f := newFixture(t)
	f.runtime.providers[0].Models[0].Name = "  "
	view, err := f.service.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	models := view.Providers[0].Models
	if len(models) != 1 || models[0].Name != "model-a" {
		t.Fatalf("模型名回退错误: %+v", models)
	}
}

// TestOperationViewsSkipsUnavailableOperation 覆盖读取当日操作时过滤掉
// 全局停用的 keepalive 记录。
func TestOperationViewsSkipsUnavailableOperation(t *testing.T) {
	f := newFixture(t)
	day := engine.BeijingDay(f.service.now())
	if _, err := f.store.DB().Exec(`
INSERT INTO daily_ops (target, account_id, operation, day_key, state, updated_at)
VALUES ('fixture', 'a1', 'keepalive', ?, 'pending', 'ts')`, day); err != nil {
		t.Fatal(err)
	}
	ops, err := f.service.operationViews(context.Background(), "fixture", "a1", day, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if op.Operation == "keepalive" {
			t.Fatalf("停用操作不应出现在视图: %+v", ops)
		}
	}
}

// TestDeleteAccountAuditFailure 覆盖账户删除失败时的审计与错误回传。
func TestDeleteAccountAuditFailure(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.store.DB().Exec(`DROP TABLE sources`); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeleteAccount(ctx, "fixture", "a1"); err == nil {
		t.Fatal("sources 表缺失时删除应失败")
	}
	var logs int
	if err := f.store.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM logs WHERE operation = 'account' AND state = 'failed_final'`).
		Scan(&logs); err != nil {
		t.Fatal(err)
	}
	if logs == 0 {
		t.Fatal("删除失败应写审计日志")
	}
}

// TestRecordUsageRejectsUpstreamError 覆盖额度记录前的上游错误拒绝。
func TestRecordUsageRejectsUpstreamError(t *testing.T) {
	f := newFixture(t)
	_, _, err := f.service.recordUsage(context.Background(), "fixture", "a1",
		providerruntime.Usage{Error: "HTTP 401 unauthorized"})
	if err == nil || !strings.Contains(err.Error(), "额度查询失败") {
		t.Fatalf("上游额度错误应被拒绝，实际 %v", err)
	}
}

// TestRunOneCheckinCaptchaObserved 覆盖签到返回验证码时的风控记录分支。
func TestRunOneCheckinCaptchaObserved(t *testing.T) {
	f := newFixture(t)
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	guard := requestguard.New(func() time.Time { return now })
	f.service.SetRiskGuard(guard)
	f.runtime.checkin = providerruntime.Checkin{Outcome: "captcha", Message: "需要验证码"}
	_ = f.service.Run(context.Background(), "fixture", "a1", "checkin")
	if _, reason, ok := guard.Cooldown("fixture", "a1"); !ok || reason != requestguard.ReasonChallenge {
		t.Fatalf("验证码应触发 challenge 冷却，reason=%v ok=%v", reason, ok)
	}
}

// TestRecordRunWriteFailures 覆盖 run 落库与日志写入失败分支。
func TestRecordRunWriteFailures(t *testing.T) {
	ctx := context.Background()
	results := []runResult{{
		Provider: "fixture", Account: "a1", Action: "checkin",
		State: string(engine.Already), Executed: false, Message: "今天已领取",
	}}

	t.Run("run_items 写入失败", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.store.DB().Exec(`DROP TABLE run_items`); err != nil {
			t.Fatal(err)
		}
		if err := f.service.recordRun(ctx, "run-items-fail", "started", "checkin", results); err == nil {
			t.Fatal("run_items 缺失时应返回错误")
		}
	})
	t.Run("审计日志写入失败", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.store.DB().Exec(`DROP TABLE logs`); err != nil {
			t.Fatal(err)
		}
		if err := f.service.recordRun(ctx, "logs-fail", "started", "checkin", results); err == nil {
			t.Fatal("logs 缺失时应返回错误")
		}
	})
}

// TestAuditHelperNilAndEmptyBranches 覆盖审计辅助函数的 nil/空值防御分支。
func TestAuditHelperNilAndEmptyBranches(t *testing.T) {
	ctx := context.Background()
	var nilService *Service
	nilService.writeAuditLog(ctx, "fixture", "a1", "account", "completed", "info", "ok")
	nilService.writeAuditFailure(ctx, "fixture", "a1", "account", nil)
	nilService.writeModelTestAuditLog(ctx, "fixture", "a1", "model-a", ModelTestView{}, nil)

	f := newFixture(t)
	// ctx 为 nil 时不应 panic。
	f.service.writeAuditLog(nil, "fixture", "a1", "account", "completed", "info", "ok")
	f.service.writeAuditFailure(ctx, "fixture", "a1", "account", errors.New("boom"))
	if err := f.service.insertLog(ctx, nil, "ts", "fixture", "a1", "account", "ok", "info", "x"); err != nil {
		t.Fatalf("nil execer 应直接返回 nil: %v", err)
	}
}

// TestWriteModelTestAuditLogLatencyBranches 覆盖模型测试日志里的耗时与状态分支。
func TestWriteModelTestAuditLogLatencyBranches(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.service.writeModelTestAuditLog(ctx, "fixture", "a1", "model-a",
		ModelTestView{Status: 429, Latency: 12, Message: "请求被限流"}, nil)
	f.service.writeModelTestAuditLog(ctx, "fixture", "a1", "model-a",
		ModelTestView{OK: true, Status: 200, Latency: 8}, nil)

	rows, err := f.store.DB().QueryContext(ctx,
		`SELECT message FROM logs WHERE operation = 'model_test' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var messages []string
	for rows.Next() {
		var message string
		if err := rows.Scan(&message); err != nil {
			t.Fatal(err)
		}
		messages = append(messages, message)
	}
	if len(messages) != 2 ||
		!strings.Contains(messages[0], "HTTP 429") || !strings.Contains(messages[0], "12 ms") ||
		!strings.Contains(messages[1], "8 ms") {
		t.Fatalf("模型测试日志内容错误: %v", messages)
	}
}

// TestRunResultAuditBranches 覆盖运行结果审计的空状态、人工处理与模型字段。
func TestRunResultAuditBranches(t *testing.T) {
	state, level, message := runResultAudit(runResult{Action: "checkin", Executed: true})
	if state != "completed" || level != "info" {
		t.Fatalf("空状态审计 = %s/%s/%s", state, level, message)
	}
	state, level, _ = runResultAudit(runResult{
		Action: "checkin", State: string(engine.ManualRequired), Executed: false,
	})
	if state != string(engine.ManualRequired) || level != "warn" {
		t.Fatalf("人工处理审计 = %s/%s", state, level)
	}
	credits, balance := 5.0, 5.0
	_, _, message = runResultAudit(runResult{
		Action: "model_test", State: "completed", Executed: true,
		Model: "model-a", Credits: &credits, Balance: &balance,
	})
	if !strings.Contains(message, "模型 model-a") {
		t.Fatalf("模型审计文案 = %s", message)
	}
}

// TestKeepaliveStreamRetryError 覆盖保活流式重试时的网络失败分支。
func TestKeepaliveStreamRetryError(t *testing.T) {
	f := newFixture(t)
	f.runtime.fetchQueue = []providerruntime.FetchResponse{
		{Status: 400, Body: []byte(`{"error":"non-stream is not supported"}`)},
	}
	f.runtime.fetchErr = errors.New("network down")
	result, err := f.service.keepalive(context.Background(), f.runtime, "fixture", "a1")
	if err != nil || result.Kind != engine.RRetryable ||
		!strings.Contains(result.Message, "流式保活请求失败") {
		t.Fatalf("流式保活失败处理错误: %+v %v", result, err)
	}
}

// TestHasReplyNDJSON 覆盖流式（NDJSON）回复的解析分支。
func TestHasReplyNDJSON(t *testing.T) {
	body := []byte("event: message\ndata: [DONE]\ndata: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n")
	if !hasReply(body) {
		t.Fatal("NDJSON 中的 choices 应识别为有回复")
	}
	// 非 JSON 文本按约定回退为「有任何非空内容」才算回复。
	if !hasReply([]byte("partial text without json")) {
		t.Fatal("非 JSON 回退应识别为有回复")
	}
}

// TestReconcileGrowthBranches 覆盖签到补读的缓存命中、记录失败与状态回写失败。
func TestReconcileGrowthBranches(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, engine.Beijing)
	viewFor := func(credits, balance float64) *View {
		return &View{Providers: []ProviderView{{
			ID: "fixture", Enabled: true, Checkin: true,
			Accounts: []AccountView{{
				Key: "a1", Enabled: true,
				Operations: []OperationView{{
					Operation: "checkin", State: string(engine.Already),
					Credits: &credits, Balance: &balance,
				}},
			}},
		}}}
	}
	insertOp := func(t *testing.T, f *fixture, credits, balance float64) {
		t.Helper()
		day := engine.BeijingDay(now)
		if _, err := f.store.DB().Exec(`
INSERT INTO daily_ops (target, account_id, operation, day_key, state, credits, balance,
  credits_known, balance_known, updated_at)
VALUES ('fixture', 'a1', 'checkin', ?, 'already', ?, ?, 1, 1, 'ts')`,
			day, credits, balance); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("内存缓存命中", func(t *testing.T) {
		f := newFixture(t)
		f.service.now = func() time.Time { return now }
		day := engine.BeijingDay(now)
		f.service.creditSynced = map[string]bool{day + "\x00fixture\x00a1": true}
		f.service.reconcileMissingCheckinGrowth(ctx, f.runtime, viewFor(12.5, 12.5))
		if f.runtime.checkinCalls != 0 {
			t.Fatalf("缓存命中后不应外呼，实际 %d", f.runtime.checkinCalls)
		}
	})
	t.Run("额度记录失败", func(t *testing.T) {
		f := newFixture(t)
		f.service.now = func() time.Time { return now }
		insertOp(t, f, 12.5, 12.5)
		if _, err := f.store.DB().Exec(`DROP TABLE daily_ops`); err != nil {
			t.Fatal(err)
		}
		f.runtime.checkin = providerruntime.Checkin{Outcome: "claimed", Credit: 5}
		f.service.reconcileMissingCheckinGrowth(ctx, f.runtime, viewFor(12.5, 12.5))
	})
	t.Run("状态回写失败", func(t *testing.T) {
		f := newFixture(t)
		f.service.now = func() time.Time { return now }
		insertOp(t, f, 12.5, 12.5)
		if _, err := f.store.DB().Exec(`
CREATE TRIGGER fail_message_update BEFORE UPDATE ON daily_ops
WHEN NEW.message <> OLD.message
BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
			t.Fatal(err)
		}
		f.runtime.checkin = providerruntime.Checkin{Outcome: "claimed", Credit: 5, Message: "补记"}
		f.service.reconcileMissingCheckinGrowth(ctx, f.runtime, viewFor(12.5, 12.5))
	})
}

// TestEnsureEnabledBranches 覆盖账号启用校验的空账号、数据库错误与停用分支。
func TestEnsureEnabledAccountStates(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.service.ensureEnabled(ctx, "fixture", "  "); err != nil {
		t.Fatalf("空账号应直接通过: %v", err)
	}
	if err := f.service.ensureEnabled(ctx, "fixture", "unknown"); err != nil {
		t.Fatalf("未知账号（无行）应放行给上层报错: %v", err)
	}
	if _, err := f.service.List(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.service.SetEnabled(ctx, "fixture", true); err != nil {
		t.Fatal(err)
	}
	if err := f.service.SetAccountEnabled(ctx, "fixture", "a1", false); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ensureEnabled(ctx, "fixture", "a1"); err == nil ||
		!strings.Contains(err.Error(), "账号已停用") {
		t.Fatalf("停用账号应被拒绝，实际 %v", err)
	}

	t.Run("账号查询错误", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.service.List(ctx); err != nil {
			t.Fatal(err)
		}
		if err := f.service.SetEnabled(ctx, "fixture", true); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.DB().Exec(`DROP TABLE accounts`); err != nil {
			t.Fatal(err)
		}
		if err := f.service.ensureEnabled(ctx, "fixture", "a1"); err == nil {
			t.Fatal("accounts 表缺失时应回传错误")
		}
	})
	t.Run("数据库关闭", func(t *testing.T) {
		closed := newFixture(t)
		_ = closed.store.Close()
		if err := closed.service.ensureEnabled(ctx, "fixture", "a1"); err == nil {
			t.Fatal("数据库关闭时应回传错误")
		}
	})
}

// TestModelTestAutoSelectedAccountDisabled 覆盖自动选择首个账号后该账号已停用：
// 诊断测试仍然放行，并记下这次测试用的是哪个账号。
func TestModelTestAutoSelectedAccountDisabled(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.service.List(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.service.SetEnabled(ctx, "fixture", true); err != nil {
		t.Fatal(err)
	}
	if err := f.service.SetAccountEnabled(ctx, "fixture", "a1", false); err != nil {
		t.Fatal(err)
	}
	view, err := f.service.TestModel(ctx, "fixture", "", "model-a")
	if err != nil {
		t.Fatalf("自动选中的停用账号也应允许诊断测试，实际 %v", err)
	}
	if !view.OK || view.Model != "model-a" {
		t.Fatalf("自动选中账号的诊断结果异常: %+v", view)
	}
}
