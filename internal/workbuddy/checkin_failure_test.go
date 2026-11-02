package workbuddy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newCheckinCoverageService(
	t *testing.T,
	handler http.HandlerFunc,
	now time.Time,
) (*CheckinService, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	store := newManagedCredentialStore(PendingCredential{
		Target: "workbuddy-cn", AccessToken: "checkin-access",
		RefreshToken:    "checkin-refresh",
		AccessExpiresAt: now.Add(time.Hour).UnixMilli(),
		Domain:          "copilot.tencent.com", UID: "checkin-uid",
	})
	session := NewManagedSession(ManagedSessionConfig{
		BaseURL: server.URL, Store: store, Now: func() time.Time { return now },
	})
	return NewCheckinService(CheckinConfig{Session: session, BaseURL: server.URL}), server
}

func TestCheckinConstructorDefaultsAndNilSessionFailures(t *testing.T) {
	svc := NewCheckinService(CheckinConfig{})
	if svc.baseURL != DefaultBaseURL {
		t.Fatalf("缺省 base URL 错误: %q", svc.baseURL)
	}
	if _, err := svc.Checkin(context.Background()); err == nil {
		t.Fatal("无受管会话时 Checkin 必须失败")
	}
	if _, err := svc.Review(context.Background()); err == nil {
		t.Fatal("无受管会话时 Review 必须失败")
	}
}

func TestCheckinOptionalNumberParsesRealWireValues(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		value   float64
		set     bool
		wantErr bool
	}{
		{name: "空输入", raw: "", set: false},
		{name: "null", raw: "null", set: false},
		{name: "数字", raw: "12.5", value: 12.5, set: true},
		{name: "字符串数字", raw: `" 12.5 "`, value: 12.5, set: true},
		{name: "空白字符串", raw: `"  "`, set: false},
		{name: "非法字符串", raw: `"not-a-number"`, wantErr: true},
		{name: "非法类型", raw: "true", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got optionalNumber
			err := got.UnmarshalJSON([]byte(tt.raw))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if got.Value != tt.value || got.Set != tt.set {
				t.Fatalf("解析结果错误: %+v", got)
			}
		})
	}
}

func TestSummarizeBalanceClampsOverusedPackages(t *testing.T) {
	got := summarizeBalance([]resourcePackage{{
		CycleTotalCapacity: optionalNumber{Value: 5, Set: true},
		CycleUsedCapacity:  optionalNumber{Value: 9, Set: true},
	}})
	if !got.Known || got.Remaining != 0 {
		t.Fatalf("超用余额必须夹到 0，实际 %+v", got)
	}
}

func TestCheckinUnknownBusinessCodeAndStatusFailure(t *testing.T) {
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	t.Run("未知领取业务码", func(t *testing.T) {
		target := &fakeCheckinTarget{active: true, claimCode: 9999, claimMsg: "未知失败"}
		svc, _ := newCheckinTestService(t, target, now)
		result, err := svc.Checkin(context.Background())
		if err == nil || result.Outcome != "" {
			t.Fatalf("未知业务码不得被猜成成功，result=%+v err=%v", result, err)
		}
	})
	t.Run("状态接口非 401 失败", func(t *testing.T) {
		target := &fakeCheckinTarget{active: true, statusCode: http.StatusInternalServerError}
		svc, _ := newCheckinTestService(t, target, now)
		if _, err := svc.Checkin(context.Background()); err == nil {
			t.Fatal("状态接口 500 必须返回错误")
		}
	})
}

func TestCheckinReviewFailureAndKnownBalancePaths(t *testing.T) {
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	t.Run("活动未开始", func(t *testing.T) {
		svc, _ := newCheckinCoverageService(t, func(w http.ResponseWriter, r *http.Request) {
			writeEnvelope(t, w, map[string]any{"active": false})
		}, now)
		review, err := svc.Review(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if review.Active || review.Message != "活动未开始或已结束" {
			t.Fatalf("未开始状态错误: %+v", review)
		}
	})
	t.Run("已领取并带额度", func(t *testing.T) {
		svc, _ := newCheckinCoverageService(t, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v2/billing/meter/checkin-activity-status":
				writeEnvelope(t, w, map[string]any{
					"active": true, "today_checked_in": true, "today_credit": "50",
				})
			case "/billing/meter/get-user-resource-summary":
				writeEnvelope(t, w, map[string]any{"Packages": []map[string]any{{
					"CycleTotalCapacity": "100", "CycleUsedCapacity": "25",
				}}})
			default:
				http.NotFound(w, r)
			}
		}, now)
		review, err := svc.Review(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !review.CheckedIn || review.Credit != 50 || !review.CreditKnown ||
			review.Balance != 75 || !review.BalanceKnown {
			t.Fatalf("已领取复查字段错误: %+v", review)
		}
	})
	t.Run("状态接口失败", func(t *testing.T) {
		svc, _ := newCheckinCoverageService(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}, now)
		if _, err := svc.Review(context.Background()); err == nil {
			t.Fatal("复查状态接口 500 必须返回错误")
		}
	})
	t.Run("401 后余额查询失败", func(t *testing.T) {
		svc, _ := newCheckinCoverageService(t, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v2/plugin/auth/token/refresh":
				writeEnvelope(t, w, map[string]any{
					"accessToken": "renewed", "refreshToken": "renewed-refresh", "expiresIn": 3600,
				})
			default:
				w.WriteHeader(http.StatusUnauthorized)
			}
		}, now)
		if _, err := svc.Review(context.Background()); !errors.Is(err, ErrManagedReauthRequired) {
			t.Fatalf("401 后余额仍被拒绝应要求重登，实际 %v", err)
		}
	})
}

func TestCheckinAlreadyDoneCarriesKnownBalance(t *testing.T) {
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	svc, _ := newCheckinCoverageService(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/billing/meter/checkin-activity-status":
			writeEnvelope(t, w, map[string]any{
				"active": true, "today_checked_in": true, "today_credit": 10,
			})
		case "/billing/meter/get-user-resource-summary":
			writeEnvelope(t, w, map[string]any{"Packages": []map[string]any{{
				"CycleRemainCapacity": "20",
			}}})
		default:
			http.NotFound(w, r)
		}
	}, now)
	result, err := svc.Checkin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != CheckinDone || result.Balance != 20 || !result.BalanceKnown {
		t.Fatalf("已完成签到应带余额，实际 %+v", result)
	}
}

func TestCheckinReadBalanceRealHTTPFailures(t *testing.T) {
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		handler http.HandlerFunc
		strict  bool
		wantErr error
	}{
		{
			name: "403 要求重登",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusForbidden)
			},
			strict: true, wantErr: ErrManagedReauthRequired,
		},
		{
			name: "500 可重试",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			strict: true, wantErr: ErrManagedRetryable,
		},
		{
			name: "响应超限",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(strings.Repeat("x", maxResponseSize+1)))
			},
			strict: true, wantErr: ErrManagedRetryable,
		},
		{
			name: "响应非法 JSON",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("{"))
			},
			strict: true, wantErr: ErrManagedRetryable,
		},
		{
			name: "业务码失败",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"code":123,"data":{}}`))
			},
			strict: true, wantErr: ErrManagedRetryable,
		},
		{
			name: "套餐数据非法",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"code":0,"data":{"Packages":"bad"}}`))
			},
			strict: true, wantErr: ErrManagedRetryable,
		},
		{
			name: "非严格模式暴露 401",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v2/plugin/auth/token/refresh" {
					writeEnvelope(t, w, map[string]any{
						"accessToken": "next", "refreshToken": "next-refresh", "expiresIn": 3600,
					})
					return
				}
				w.WriteHeader(http.StatusUnauthorized)
			},
			strict: false, wantErr: ErrManagedReauthRequired,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newCheckinCoverageService(t, tt.handler, now)
			_, err := svc.readBalanceRequest(context.Background(), tt.strict)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err=%v want=%v", err, tt.wantErr)
			}
		})
	}

	t.Run("请求构造失败", func(t *testing.T) {
		svc := NewCheckinService(CheckinConfig{BaseURL: "http://[::1"})
		if _, err := svc.readBalance(context.Background()); err == nil {
			t.Fatal("非法 base URL 应使请求构造失败")
		}
	})

	t.Run("403 来源状态更新失败", func(t *testing.T) {
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer target.Close()
		credential := validRefreshCredential(now)
		store := &hookManagedStore{
			load: func(context.Context) ([]byte, error) {
				return credentialBytes(t, credential), nil
			},
			reauth: func(context.Context) error {
				return errors.New("source state unavailable")
			},
		}
		session := NewManagedSession(ManagedSessionConfig{
			BaseURL: target.URL, Store: store, Now: func() time.Time { return now },
		})
		svc := NewCheckinService(CheckinConfig{Session: session, BaseURL: target.URL})
		_, err := svc.readBalance(context.Background())
		if !errors.Is(err, ErrManagedReauthRequired) || !strings.Contains(err.Error(), "来源状态更新失败") {
			t.Fatalf("来源状态更新失败应保留重登分类，实际 %v", err)
		}
	})
}

func TestCheckinCallFailureResponses(t *testing.T) {
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{name: "非 2xx 业务码", status: http.StatusInternalServerError,
			body: `{"code":999,"msg":"busy"}`, wantErr: "busy"},
		{name: "非 2xx 无业务码", status: http.StatusBadGateway,
			wantErr: "非 2xx"},
		{name: "2xx 业务码", status: http.StatusOK,
			body: `{"code":999,"msg":"busy"}`, wantErr: "busy"},
		{name: "缺数据", status: http.StatusOK,
			body: `{"code":0}`, wantErr: "缺少数据"},
		{name: "非法数据", status: http.StatusOK,
			body: `{"code":0,"data":"bad"}`, wantErr: "格式无效"},
		{name: "响应超限", status: http.StatusOK,
			body: strings.Repeat("x", maxResponseSize+1), wantErr: "无法读取"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newCheckinCoverageService(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}, now)
			var dst checkinActivity
			status := 0
			err := svc.call(context.Background(), "/test", &dst, &status)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err=%v want substring %q", err, tt.wantErr)
			}
		})
	}

	t.Run("请求构造失败", func(t *testing.T) {
		svc := NewCheckinService(CheckinConfig{BaseURL: "http://[::1"})
		var dst checkinActivity
		status := 0
		if err := svc.call(context.Background(), "/test", &dst, &status); err == nil {
			t.Fatal("非法 base URL 应使请求构造失败")
		}
	})

	t.Run("受管会话失败", func(t *testing.T) {
		svc := NewCheckinService(CheckinConfig{Session: NewManagedSession(ManagedSessionConfig{})})
		var dst checkinActivity
		status := 0
		if err := svc.call(context.Background(), "/test", &dst, &status); err == nil {
			t.Fatal("无受管会话时 call 必须失败")
		}
	})

	t.Run("响应读取中断", func(t *testing.T) {
		svc, _ := newCheckinCoverageService(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("short"))
		}, now)
		var dst checkinActivity
		status := 0
		if err := svc.call(context.Background(), "/test", &dst, &status); err == nil {
			t.Fatal("截断响应必须返回读取错误")
		}
	})
}

func TestCheckinBusinessErrorMessages(t *testing.T) {
	if got := (&checkinBusinessError{Message: "上游拒绝"}).Error(); got != "上游拒绝" {
		t.Fatalf("业务消息未保留: %q", got)
	}
	if got := (&checkinBusinessError{}).Error(); got == "" {
		t.Fatal("空业务消息应有稳定兜底")
	}
}

func TestCheckinUnknownBusinessJSONRemainsError(t *testing.T) {
	var activity checkinActivity
	if err := json.Unmarshal([]byte(`{"active":"not-bool"}`), &activity); err == nil {
		t.Fatal("测试夹具必须产生解析错误")
	}
}
