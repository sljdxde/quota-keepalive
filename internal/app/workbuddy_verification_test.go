package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"qka/internal/accounts"
	"qka/internal/capabilities"
	"qka/internal/store"
	"qka/internal/workbuddy"
)

type appSecretStore struct {
	mu     sync.Mutex
	values map[string]string
}

func newAppSecretStore() *appSecretStore {
	return &appSecretStore{values: map[string]string{}}
}

func (s *appSecretStore) Set(_ context.Context, service, account, secret string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[service+"/"+account] = secret
	return nil
}

func (s *appSecretStore) Get(_ context.Context, service, account string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	secret, ok := s.values[service+"/"+account]
	if !ok {
		return "", capabilities.ErrSecretNotFound
	}
	return secret, nil
}

func (s *appSecretStore) Delete(_ context.Context, service, account string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, service+"/"+account)
	return nil
}

type failingAppSecretStore struct{}

func (failingAppSecretStore) Set(context.Context, string, string, string) error {
	return errors.New("storage unavailable")
}

func (failingAppSecretStore) Get(context.Context, string, string) (string, error) {
	return "", capabilities.ErrSecretNotFound
}

func (failingAppSecretStore) Delete(context.Context, string, string) error {
	return nil
}

func TestWorkBuddyVerificationSinkPersistsCredentialAndPreservesExistingState(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	acct := accounts.NewService(st.DB())
	file := newAppSecretStore()
	creds := capabilities.NewCredentialService(
		failingAppSecretStore{},
		file,
		capabilities.Grant{
			"credentials.managed": true,
			"credentials.resolve": true,
		},
		nil,
	)
	sink := newWorkBuddyVerificationSink(acct, creds)
	ctx := context.Background()
	verified := workbuddy.VerifiedAccount{
		Target: "workbuddy-cn", AccountID: "wb_stable", SourceID: "wbm_stable",
		DisplayName: "****3456", MaskedIdentity: "****3456",
	}
	credential := []byte(`{"access_token":"managed-access","refresh_token":"managed-refresh"}`)

	if err := sink.AcceptVerifiedAccount(ctx, verified, credential); err != nil {
		t.Fatalf("保存已验证账号失败: %v", err)
	}
	acc, err := acct.Get(ctx, "workbuddy-cn", verified.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if acc.Enabled {
		t.Fatal("首次验证通过的账号必须默认停用")
	}
	if acc.DisplayName != "****3456" || acc.MaskedIdentity != "****3456" {
		t.Errorf("账号展示必须只含脱敏身份: %+v", acc)
	}
	if len(acc.Sources) != 1 || acc.Sources[0].Kind != accounts.KindManaged ||
		acc.Sources[0].Health != accounts.HealthHealthy {
		t.Errorf("应建立健康 managed 来源: %+v", acc.Sources)
	}
	list, err := acct.List(ctx, "workbuddy-cn")
	if err != nil {
		t.Fatal(err)
	}
	publicJSON, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(publicJSON), "managed-access") ||
		strings.Contains(string(publicJSON), "managed-refresh") {
		t.Errorf("账号/MCP 投影不得包含凭据: %s", publicJSON)
	}

	resolved, err := creds.Resolve(ctx, capabilities.CredentialRef{
		Target: "workbuddy-cn", AccountID: verified.AccountID,
		SourceID: verified.SourceID, Kind: capabilities.Managed,
	})
	if err != nil {
		t.Fatalf("受管凭据未保存: %v", err)
	}
	if resolved.Secret != string(credential) {
		t.Error("受管凭据内容不一致")
	}

	if err := acct.SetEnabled(ctx, "workbuddy-cn", verified.AccountID, true); err != nil {
		t.Fatal(err)
	}
	if err := acct.PinSource(ctx, "workbuddy-cn", verified.AccountID, verified.SourceID, true); err != nil {
		t.Fatal(err)
	}
	if err := acct.MarkSourceFailure(ctx, "workbuddy-cn", verified.AccountID, verified.SourceID); err != nil {
		t.Fatal(err)
	}
	if err := sink.AcceptVerifiedAccount(ctx, verified, credential); err != nil {
		t.Fatalf("重复验证失败: %v", err)
	}

	acc, err = acct.Get(ctx, "workbuddy-cn", verified.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if !acc.Enabled {
		t.Error("重复验证不得改写已有账号启用状态")
	}
	if len(acc.Sources) != 1 || !acc.Sources[0].Pinned || acc.Sources[0].Health != accounts.HealthHealthy {
		t.Errorf("重复验证应保留固定状态并恢复健康: %+v", acc.Sources)
	}
}

func TestWorkBuddyVerificationSinkDoesNotCreateAccountWhenCredentialPersistFails(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	acct := accounts.NewService(st.DB())
	creds := capabilities.NewCredentialService(
		failingAppSecretStore{},
		failingAppSecretStore{},
		capabilities.Grant{"credentials.managed": true},
		nil,
	)
	sink := newWorkBuddyVerificationSink(acct, creds)
	err = sink.AcceptVerifiedAccount(context.Background(), workbuddy.VerifiedAccount{
		Target: "workbuddy-cn", AccountID: "wb_failed", SourceID: "wbm_failed",
		DisplayName: "****0000", MaskedIdentity: "****0000",
	}, []byte(`{"access_token":"secret"}`))
	if err == nil {
		t.Fatal("凭据持久化失败时不得报告成功")
	}
	list, err := acct.List(context.Background(), "workbuddy-cn")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("凭据持久化失败时不得创建账号或来源: %+v", list)
	}
}
