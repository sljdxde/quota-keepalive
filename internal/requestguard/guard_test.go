package requestguard

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDoMergesRepeatedAndConcurrentCalls(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	guard := New(func() time.Time { return now })
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	fn := func() (string, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return "ok", nil
	}

	var wg sync.WaitGroup
	results := make([]string, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		results[0], _ = Do(context.Background(), guard, "p", "a", "refresh", time.Minute, fn)
	}()
	<-started
	go func() {
		defer wg.Done()
		results[1], _ = Do(context.Background(), guard, "p", "a", "refresh", time.Minute, fn)
	}()
	close(release)
	wg.Wait()
	if calls.Load() != 1 || results[0] != "ok" || results[1] != "ok" {
		t.Fatalf("calls=%d results=%v", calls.Load(), results)
	}

	now = now.Add(10 * time.Second)
	got, err := Do(context.Background(), guard, "p", "a", "refresh", time.Minute, fn)
	if err != nil || got != "ok" || calls.Load() != 1 {
		t.Fatalf("repeat not merged: got=%q calls=%d err=%v", got, calls.Load(), err)
	}
}

func TestDifferentOperationsForSameAccountAreSerialized(t *testing.T) {
	guard := New(time.Now)
	var active atomic.Int32
	var maxActive atomic.Int32
	fn := func() (bool, error) {
		current := active.Add(1)
		for {
			old := maxActive.Load()
			if current <= old || maxActive.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		active.Add(-1)
		return true, nil
	}
	var wg sync.WaitGroup
	for _, operation := range []string{"checkin", "refresh", "model_test"} {
		operation := operation
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = Do(context.Background(), guard, "p", "a", operation, 0, fn)
		}()
	}
	wg.Wait()
	if maxActive.Load() != 1 {
		t.Fatalf("same account calls overlapped: %d", maxActive.Load())
	}
}

func TestRateLimitAndChallengeCooldownsBlockAllOperations(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	guard := New(func() time.Time { return now })
	_, err := Do(context.Background(), guard, "p", "a", "refresh", 0, func() (bool, error) {
		return false, errors.New("HTTP 429 too many requests")
	})
	if err == nil {
		t.Fatal("expected upstream error")
	}
	_, err = Do(context.Background(), guard, "p", "a", "model_test", 0, func() (bool, error) {
		t.Fatal("cooling account must not execute")
		return true, nil
	})
	var blocked *BlockedError
	if !errors.As(err, &blocked) || blocked.Reason != ReasonRateLimit {
		t.Fatalf("want rate-limit cooldown, got %v", err)
	}
	if until, reason, ok := guard.Cooldown("p", "a"); !ok ||
		reason != ReasonRateLimit || !until.Equal(now.Add(15*time.Minute)) {
		t.Fatalf("cooldown = %v %v %v", until, reason, ok)
	}

	guard.Reset()
	guard.ObserveText("p", "a", "验证码 required")
	until, reason, ok := guard.Cooldown("p", "a")
	if !ok || reason != ReasonChallenge || !until.Equal(now.Add(6*time.Hour)) {
		t.Fatalf("challenge cooldown = %v %v %v", until, reason, ok)
	}
}

func TestBackoffGrowsAndCaps(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	guard := New(func() time.Time { return now })
	guard.Record("p", "a", ReasonRateLimit)
	first, _, _ := guard.Cooldown("p", "a")
	guard.Record("p", "a", ReasonRateLimit)
	second, _, _ := guard.Cooldown("p", "a")
	if second.Sub(first) != 15*time.Minute {
		t.Fatalf("expected 15m backoff growth, got %v", second.Sub(first))
	}
	for i := 0; i < 20; i++ {
		guard.Record("p", "a", ReasonRateLimit)
	}
	last, _, _ := guard.Cooldown("p", "a")
	if got := last.Sub(now); got != 6*time.Hour {
		t.Fatalf("cooldown cap = %v", got)
	}
}

func TestUnknownErrorsAndSuccessDoNotCoolDown(t *testing.T) {
	guard := New(time.Now)
	_, _ = Do(context.Background(), guard, "p", "a", "refresh", 0, func() (bool, error) {
		return false, errors.New("database is locked")
	})
	if _, _, ok := guard.Cooldown("p", "a"); ok {
		t.Fatal("generic error must not cause upstream cooldown")
	}
	_, err := Do(context.Background(), guard, "p", "a", "refresh", 0, func() (bool, error) {
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := guard.Cooldown("p", "a"); ok {
		t.Fatal("success must not cool down")
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]Reason{
		"HTTP 401 invalid token": ReasonAuth,
		"HTTP 403 forbidden":     ReasonAuth,
		"HTTP 429 too many":      ReasonRateLimit,
		"captcha required":       ReasonChallenge,
		"HTTP 503":               ReasonUpstream,
		"ordinary failure":       "",
	}
	for input, want := range cases {
		if got := Classify(input); got != want {
			t.Errorf("Classify(%q)=%q want %q", input, got, want)
		}
	}
	if !strings.Contains((&BlockedError{Reason: ReasonAuth}).Error(), "登录态") {
		t.Fatal("blocked error should be user-readable")
	}
}

func TestGuardNilAndDefensivePaths(t *testing.T) {
	if New(nil).now == nil {
		t.Fatal("nil clock should default to time.Now")
	}
	got, err := Do(context.Background(), nil, "p", "a", "op", 0, func() (int, error) {
		return 7, nil
	})
	if err != nil || got != 7 {
		t.Fatalf("nil guard should execute directly: %d %v", got, err)
	}

	var nilGuard *Guard
	nilGuard.ObserveError("p", "a", errors.New("HTTP 429"))
	nilGuard.ObserveText("p", "a", "HTTP 429")
	nilGuard.ObserveStatus("p", "a", 429)
	nilGuard.Record("p", "a", ReasonAuth)
	nilGuard.Reset()
	if _, _, ok := nilGuard.Cooldown("p", "a"); ok {
		t.Fatal("nil guard cannot cool down")
	}

	guard := New(time.Now)
	guard.ObserveError("p", "a", nil)
	guard.ObserveText("p", "a", "ordinary")
	guard.Record("p", "a", "")
	if _, _, ok := guard.Cooldown("p", "a"); ok {
		t.Fatal("empty reason must not cool down")
	}
	if !strings.Contains((&BlockedError{}).Error(), "请求冷却中") ||
		!strings.Contains((&BlockedError{Reason: ReasonUpstream}).Error(), "远端") {
		t.Fatal("blocked labels are incomplete")
	}
	until := time.Date(2026, 11, 2, 14, 5, 0, 0, time.UTC)
	if got := (&BlockedError{Reason: ReasonRateLimit, Until: until}).Error(); !strings.Contains(got, until.Local().Format("15:04")) {
		t.Fatalf("blocked error should include cooldown time: %q", got)
	}
	if got := Classify(" \t"); got != "" {
		t.Fatalf("blank text classified as %q", got)
	}
	if IsBlocked(errors.New("ordinary")) {
		t.Fatal("ordinary error is not blocked")
	}
}

func TestGuardContextCancellationAndStatusClasses(t *testing.T) {
	guard := New(time.Now)
	state := guard.scope("p", "a")
	state.gate <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Do(ctx, guard, "p", "a", "blocked", 0, func() (bool, error) {
		t.Fatal("canceled waiter must not execute")
		return true, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context canceled, got %v", err)
	}
	<-state.gate

	for status, want := range map[int]Reason{
		401: ReasonAuth, 403: ReasonAuth, 429: ReasonRateLimit,
		408: ReasonUpstream, 500: ReasonUpstream,
	} {
		guard.Reset()
		guard.ObserveStatus("p", "a", status)
		if _, reason, ok := guard.Cooldown("p", "a"); !ok || reason != want {
			t.Errorf("status %d cooldown = %v %v", status, reason, ok)
		}
	}
	guard.Reset()
	guard.ObserveStatus("p", "a", 400)
	if _, _, ok := guard.Cooldown("p", "a"); ok {
		t.Fatal("HTTP 400 must not trigger cooldown")
	}
}

func TestCooldownDepthsAndExpiry(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	guard := New(func() time.Time { return now })
	if got := cooldownFor(ReasonAuth, 0); got != 30*time.Minute {
		t.Fatalf("auth floor = %v", got)
	}
	if got := cooldownFor("unknown", 1); got != 0 {
		t.Fatalf("unknown cooldown = %v", got)
	}
	guard.Record("p", "a", ReasonUpstream)
	now = now.Add(time.Hour + time.Minute)
	if _, _, ok := guard.Cooldown("p", "a"); ok {
		t.Fatal("expired cooldown should clear")
	}
	if got := Classify("HTTP 408"); got != ReasonUpstream {
		t.Fatalf("HTTP 408 classified as %q", got)
	}
}

func TestCachedResultOfAnotherTypeUsesZeroValue(t *testing.T) {
	guard := New(time.Now)
	_, _ = Do(context.Background(), guard, "p", "a", "shared", time.Minute,
		func() (int, error) { return 3, nil })
	got, err := Do(context.Background(), guard, "p", "a", "shared", time.Minute,
		func() (string, error) { return "new", nil })
	if err != nil || got != "" {
		t.Fatalf("type-mismatched cache = %q %v", got, err)
	}
}
