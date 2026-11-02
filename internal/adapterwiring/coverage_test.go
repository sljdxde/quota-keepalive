package adapterwiring

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"qka/internal/capabilities"
	"qka/internal/engine"
	"qka/internal/store"
)

type hostFunc func(context.Context, string, map[string]any) (json.RawMessage, error)

func (f hostFunc) CallWith(
	ctx context.Context,
	op string,
	args map[string]any,
) (json.RawMessage, error) {
	return f(ctx, op, args)
}

type fakeCheckinEngine struct {
	run       func(context.Context, engine.Key, engine.Executor) (engine.Observation, error)
	reconcile func(context.Context, engine.Key, engine.State, string) error
}

func (f *fakeCheckinEngine) Run(
	ctx context.Context,
	key engine.Key,
	exec engine.Executor,
) (engine.Observation, error) {
	return f.run(ctx, key, exec)
}

func (f *fakeCheckinEngine) ReconcileState(
	ctx context.Context,
	key engine.Key,
	state engine.State,
	message string,
) error {
	return f.reconcile(ctx, key, state, message)
}

func TestCheckinRunnerCoveragePaths(t *testing.T) {
	if runner := NewCheckinRunner(nil, nil, nil, ""); runner.now == nil {
		t.Fatal("nil clock 应默认使用当前时间")
	}

	key := engine.Key{Target: "target", AccountID: "account", Operation: engine.Checkin}

	t.Run("引擎失败", func(t *testing.T) {
		sentinel := errors.New("engine failed")
		eng := &fakeCheckinEngine{
			run: func(
				context.Context,
				engine.Key,
				engine.Executor,
			) (engine.Observation, error) {
				return engine.Observation{}, sentinel
			},
		}
		runner := &CheckinRunner{eng: eng, host: hostFunc(nil), now: time.Now}
		if _, err := runner.Run(context.Background(), key); !errors.Is(err, sentinel) {
			t.Fatalf("应透传引擎错误，实际 %v", err)
		}
	})

	t.Run("宿主失败转为可重试结果", func(t *testing.T) {
		eng := &fakeCheckinEngine{
			run: func(
				ctx context.Context,
				key engine.Key,
				exec engine.Executor,
			) (engine.Observation, error) {
				result, err := exec(ctx, key)
				if err != nil {
					t.Fatal(err)
				}
				return engine.Observation{
					State:    engine.FailedRetryable,
					Executed: true,
					Message:  result.Message,
				}, nil
			},
		}
		runner := &CheckinRunner{
			eng: eng,
			host: hostFunc(func(context.Context, string, map[string]any) (json.RawMessage, error) {
				return nil, errors.New("adapter unavailable")
			}),
			now: time.Now,
		}
		obs, err := runner.Run(context.Background(), key)
		if err != nil {
			t.Fatal(err)
		}
		if obs.State != engine.FailedRetryable ||
			obs.Message != "adapter unavailable" {
			t.Fatalf("宿主失败应转可重试结果，实际 %+v", obs)
		}
	})

	t.Run("状态修正失败", func(t *testing.T) {
		sentinel := errors.New("reconcile failed")
		eng := &fakeCheckinEngine{
			run: func(
				ctx context.Context,
				key engine.Key,
				exec engine.Executor,
			) (engine.Observation, error) {
				if _, err := exec(ctx, key); err != nil {
					t.Fatal(err)
				}
				return engine.Observation{
					State:    engine.SignedProvisional,
					Executed: true,
					Message:  "provisional",
				}, nil
			},
			reconcile: func(
				context.Context,
				engine.Key,
				engine.State,
				string,
			) error {
				return sentinel
			},
		}
		runner := &CheckinRunner{
			eng: eng,
			host: hostFunc(func(context.Context, string, map[string]any) (json.RawMessage, error) {
				return json.RawMessage(`{
					"balanceBefore": 100,
					"balanceAfter": 200,
					"receipt": "proof"
				}`), nil
			}),
			now: func() time.Time {
				return time.Date(2026, 10, 30, 14, 0, 0, 0,
					time.FixedZone("Asia/Shanghai", 8*3600))
			},
			endpoint: "http://127.0.0.1/checkin",
		}
		if _, err := runner.Run(context.Background(), key); !errors.Is(err, sentinel) {
			t.Fatalf("应透传状态修正错误，实际 %v", err)
		}
	})
}

func TestAdapterWiringHookFailures(t *testing.T) {
	fetch := capabilities.NewHTTPFetch(
		capabilities.AllowedHostsFromManifest([]string{"allowed.example"}),
		capabilities.GrantFromNeeds([]string{"http.outbound"}),
	)
	fetchMux := NewHookMux()
	RegisterHTTPFetch(fetchMux, fetch)
	fetchHook := fetchMux.hooks["http.fetch"]
	if _, err := fetchHook(context.Background(), json.RawMessage(`{`)); err == nil {
		t.Fatal("非法 HTTP 钩子参数应失败")
	}
	if _, err := fetchHook(context.Background(), json.RawMessage(
		`{"method":"GET","url":"http://blocked.example"}`,
	)); !errors.Is(err, capabilities.ErrHostNotAllowed) {
		t.Fatalf("未授权主机应失败，实际 %v", err)
	}

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	logMux := NewHookMux()
	RegisterLogWrite(logMux, st.DB())
	logHook := logMux.hooks["log.write"]
	if _, err := logHook(context.Background(), json.RawMessage(`{`)); err == nil {
		t.Fatal("非法日志钩子参数应失败")
	}
	if _, err := logHook(context.Background(), json.RawMessage(
		`{"target":"../unsafe","accountId":"a","operation":"checkin"}`,
	)); !errors.Is(err, capabilities.ErrUnsafeNamespace) {
		t.Fatalf("非法日志命名空间应失败，实际 %v", err)
	}

	if _, err := NewHookMux().HandleHook(context.Background(), "missing", nil); err == nil {
		t.Fatal("未注册钩子应失败")
	}
}

func TestExecutorsCoverStatusAndErrorBranches(t *testing.T) {
	cases := []struct {
		name     string
		result   string
		err      error
		wantKind engine.ResultKind
	}{
		{"already", `{"status":"already"}`, nil, engine.RAlready},
		{"none", `{"status":"none"}`, nil, engine.RNone},
		{"error", "", errors.New("failed"), engine.RRetryable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host := hostFunc(func(context.Context, string, map[string]any) (json.RawMessage, error) {
				if tc.err != nil {
					return nil, tc.err
				}
				return json.RawMessage(tc.result), nil
			})
			got, err := CheckinExecutor(host)(
				context.Background(),
				engine.Key{AccountID: "a"},
			)
			if err != nil {
				t.Fatal(err)
			}
			if got.Kind != tc.wantKind {
				t.Fatalf("期望 %s，实际 %s", tc.wantKind, got.Kind)
			}
		})
	}
}
