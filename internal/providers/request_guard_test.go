package providers

import (
	"context"
	"errors"
	"testing"
	"time"

	"qka/internal/providerruntime"
	"qka/internal/requestguard"
)

func TestRefreshUsageMergesRepeatedClicks(t *testing.T) {
	f := newFixture(t)
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	f.service.SetRiskGuard(requestguard.New(func() time.Time { return now }))

	if _, err := f.service.RefreshUsage(context.Background(), "fixture", "a1"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * time.Second)
	if _, err := f.service.RefreshUsage(context.Background(), "fixture", "a1"); err != nil {
		t.Fatal(err)
	}
	if f.runtime.usageCalls != 1 {
		t.Fatalf("repeated refresh should share one request, got %d", f.runtime.usageCalls)
	}
}

func TestRateLimitCoolsDownAllOperationsForAccount(t *testing.T) {
	f := newFixture(t)
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	guard := requestguard.New(func() time.Time { return now })
	f.service.SetRiskGuard(guard)
	f.runtime.usage = providerruntime.Usage{Error: "HTTP 429 too many requests"}

	if _, err := f.service.RefreshUsage(context.Background(), "fixture", "a1"); err == nil {
		t.Fatal("expected upstream rate-limit error")
	}
	_, err := f.service.TestModel(context.Background(), "fixture", "a1", "model-a")
	var blocked *requestguard.BlockedError
	if !errors.As(err, &blocked) || blocked.Reason != requestguard.ReasonRateLimit {
		t.Fatalf("want shared rate-limit cooldown, got %v", err)
	}
	if f.runtime.usageCalls != 1 || f.runtime.fetchCalls != 0 {
		t.Fatalf("cooling account still called upstream: usage=%d fetch=%d",
			f.runtime.usageCalls, f.runtime.fetchCalls)
	}
}

func TestModelAuthFailureCoolsDownAccount(t *testing.T) {
	f := newFixture(t)
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	guard := requestguard.New(func() time.Time { return now })
	f.service.SetRiskGuard(guard)
	f.runtime.fetch = providerruntime.FetchResponse{
		Status: 401, Body: []byte(`{"error":"invalid token"}`),
	}

	view, err := f.service.TestModel(context.Background(), "fixture", "a1", "model-a")
	if err != nil || view.Status != 401 {
		t.Fatalf("first model test: %+v %v", view, err)
	}
	now = now.Add(time.Second)
	_, err = f.service.TestModel(context.Background(), "fixture", "a1", "model-a")
	var blocked *requestguard.BlockedError
	if !errors.As(err, &blocked) || blocked.Reason != requestguard.ReasonAuth {
		t.Fatalf("want auth cooldown, got %v", err)
	}
	if f.runtime.fetchCalls != 1 {
		t.Fatalf("repeated auth probe reached upstream %d times", f.runtime.fetchCalls)
	}
}

// TestModelTestDedupesPerModel 覆盖缺陷：去重键曾经只有 provider+account+
// operation，30 秒内换模型测试会直接拿到上一个模型的结果。
func TestModelTestDedupesPerModel(t *testing.T) {
	f := newFixture(t)
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	f.service.SetRiskGuard(requestguard.New(func() time.Time { return now }))
	f.runtime.providers[0].Models = append(f.runtime.providers[0].Models, providerruntime.Model{
		ID: "model-b", Name: "Model B", NPM: "@ai-sdk/openai-compatible",
		APIURL: "https://provider.example/v1",
	})
	ctx := context.Background()

	first, err := f.service.TestModel(ctx, "fixture", "a1", "model-a")
	if err != nil || !first.OK || f.runtime.fetchCalls != 1 {
		t.Fatalf("first model test: %+v %v fetch=%d", first, err, f.runtime.fetchCalls)
	}
	// 同一模型 30 秒内重复点击共享结果，不重复外呼。
	repeated, err := f.service.TestModel(ctx, "fixture", "a1", "model-a")
	if err != nil || repeated.Model != "model-a" || f.runtime.fetchCalls != 1 {
		t.Fatalf("repeated same model: %+v %v fetch=%d", repeated, err, f.runtime.fetchCalls)
	}
	// 换模型必须真正发起一次新的上游请求，并返回该模型自己的结果。
	second, err := f.service.TestModel(ctx, "fixture", "a1", "model-b")
	if err != nil || !second.OK || second.Model != "model-b" {
		t.Fatalf("different model test: %+v %v", second, err)
	}
	if f.runtime.fetchCalls != 2 {
		t.Fatalf("换模型测试应发起新的上游请求，fetch=%d", f.runtime.fetchCalls)
	}
}

// TestGrowthBackfillGoesThroughRiskGuard 覆盖：checkin 补读取值也必须经过
// 共享风控闸门，上游 429 时账号进入冷却且不会继续放大外呼。
func TestGrowthBackfillGoesThroughRiskGuard(t *testing.T) {
	f := newFixture(t)
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	guard := requestguard.New(func() time.Time { return now })
	f.service.SetRiskGuard(guard)
	mustExec(t, f.store.DB(), `
INSERT INTO daily_ops
  (target, account_id, operation, day_key, state, credits, balance,
   credits_known, balance_known, message, updated_at)
VALUES
  ('fixture', 'a1', 'checkin', '2026-10-30', 'already', 12.5, 12.5,
   1, 1, '今天已领取', 'ts')`)
	f.runtime.checkinErr = errors.New("HTTP 429 too many requests")

	ctx := context.Background()
	if _, err := f.service.List(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.service.SetEnabled(ctx, "fixture", true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.List(ctx); err != nil {
		t.Fatal(err)
	}
	if f.runtime.checkinCalls != 1 {
		t.Fatalf("growth backfill calls = %d", f.runtime.checkinCalls)
	}
	until, reason, ok := guard.Cooldown("fixture", "a1")
	if !ok || reason != requestguard.ReasonRateLimit || !until.After(now) {
		t.Fatalf("429 backfill 应让账号进入限流冷却，got ok=%v reason=%v until=%v",
			ok, reason, until)
	}
}
