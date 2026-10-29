package capabilities

import (
	"context"
	"errors"
	"testing"
)

type failingSecretStore struct{}

func (failingSecretStore) Set(context.Context, string, string, string) error {
	return errors.New("backend unavailable")
}

func (failingSecretStore) Get(context.Context, string, string) (string, error) {
	return "", ErrSecretNotFound
}

func (failingSecretStore) Delete(context.Context, string, string) error {
	return ErrSecretNotFound
}

func TestLoginCredentialStoreFallsBackToProtectedFile(t *testing.T) {
	keychain := failingSecretStore{}
	file := newFakeSecretStore()
	store := NewLoginCredentialStore(keychain, file)

	payload := []byte(`{"access_token":"secret"}`)
	if err := store.Save(context.Background(), "session-1", payload); err != nil {
		t.Fatalf("Keychain 失败时应回退文件: %v", err)
	}
	if got := file.values["qka/login-session-1"]; got != string(payload) {
		t.Errorf("文件后端未保存待验证凭据，实际 %q", got)
	}
	loaded, err := store.Load(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("读取待验证凭据失败: %v", err)
	}
	if string(loaded) != string(payload) {
		t.Errorf("读取的待验证凭据不一致，实际 %q", loaded)
	}
	if err := store.Delete(context.Background(), "session-1"); err != nil {
		t.Fatalf("删除待验证凭据失败: %v", err)
	}
	if _, ok := file.values["qka/login-session-1"]; ok {
		t.Error("删除后不应保留待验证凭据")
	}
}

func TestLoginCredentialStoreRemovesStaleFileAfterKeychainSave(t *testing.T) {
	keychain := newFakeSecretStore()
	file := newFakeSecretStore()
	file.values["qka/login-session-1"] = `{"access_token":"stale"}`
	store := NewLoginCredentialStore(keychain, file)

	payload := []byte(`{"access_token":"fresh"}`)
	if err := store.Save(context.Background(), "session-1", payload); err != nil {
		t.Fatalf("保存待验证凭据失败: %v", err)
	}
	if got := keychain.values["qka/login-session-1"]; got != string(payload) {
		t.Errorf("Keychain 未保存最新凭据，实际 %q", got)
	}
	if _, ok := file.values["qka/login-session-1"]; ok {
		t.Error("Keychain 写入成功后不应保留旧的文件回退副本")
	}
}
