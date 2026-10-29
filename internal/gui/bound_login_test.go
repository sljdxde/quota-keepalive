package gui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"qka/internal/accounts"
	"qka/internal/workbuddy"
)

type loginBoundAccounts struct {
	view workbuddy.LoginView
}

func (f *loginBoundAccounts) List(context.Context) ([]accounts.Account, error) {
	return nil, nil
}
func (f *loginBoundAccounts) Discover(context.Context, string) error { return nil }
func (f *loginBoundAccounts) BeginLogin(context.Context, string) (workbuddy.LoginView, error) {
	return f.view, nil
}
func (f *loginBoundAccounts) LoginStatus(context.Context, string) workbuddy.LoginView {
	return f.view
}
func (f *loginBoundAccounts) CancelLogin(context.Context, string) (workbuddy.LoginView, error) {
	return f.view, nil
}
func (f *loginBoundAccounts) ImportSource(context.Context, string, string, string) error {
	return nil
}
func (f *loginBoundAccounts) SetEnabled(context.Context, string, string, bool) error {
	return nil
}
func (f *loginBoundAccounts) Delete(context.Context, string, string) error { return nil }
func (f *loginBoundAccounts) PinSource(context.Context, string, string, string, bool) error {
	return nil
}

func TestBoundLoginJSONExposesStateButNoCredentials(t *testing.T) {
	accts := &loginBoundAccounts{view: workbuddy.LoginView{
		SessionID: "session-1",
		Target:    "workbuddy-cn",
		Status:    workbuddy.LoginPendingVerification,
		Message:   "授权已取得，等待只读验证；尚未创建账号",
		Protocol:  workbuddy.ProtocolBaseline,
	}}
	svc := NewBoundService(Deps{Accounts: accts})

	raw, err := svc.AccountsBeginLogin("workbuddy-cn")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "access_token") || strings.Contains(string(raw), "refresh_token") ||
		strings.Contains(string(raw), `"uid"`) {
		t.Fatalf("登录状态不得包含凭据或完整身份: %s", raw)
	}
	var got workbuddy.LoginView
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != workbuddy.LoginPendingVerification || got.Protocol != workbuddy.ProtocolBaseline {
		t.Errorf("登录状态 JSON 不完整: %+v", got)
	}

	status, err := svc.AccountsLoginStatus("workbuddy-cn")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(status), "pending_verification") {
		t.Errorf("状态查询应返回同一状态，实际 %s", status)
	}
}
