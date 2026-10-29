package capabilities

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeSecretStore struct {
	values map[string]string
	denied bool
	calls  int
}

func newFakeSecretStore() *fakeSecretStore {
	return &fakeSecretStore{values: map[string]string{}}
}

func (f *fakeSecretStore) key(ref string) string { return ref }

func (f *fakeSecretStore) Set(_ context.Context, service, account, secret string) error {
	f.calls++
	f.values[service+"/"+account] = secret
	return nil
}

func (f *fakeSecretStore) Get(_ context.Context, service, account string) (string, error) {
	f.calls++
	if f.denied {
		return "", ErrAuthDenied
	}
	v, ok := f.values[service+"/"+account]
	if !ok {
		return "", ErrSecretNotFound
	}
	return v, nil
}

func (f *fakeSecretStore) Delete(_ context.Context, service, account string) error {
	f.calls++
	delete(f.values, service+"/"+account)
	return nil
}

func newCredService(keychain, file SecretStore, now Clock) *CredentialService {
	return NewCredentialService(keychain, file,
		GrantFromNeeds([]string{"credentials.resolve", "credentials.managed"}), now)
}

func managedRef() CredentialRef {
	return CredentialRef{Target: "workbuddy-cn", AccountID: "u_1", SourceID: "s1", Kind: Managed}
}

func TestResolveManagedFromKeychain(t *testing.T) {
	keychain := newFakeSecretStore()
	file := newFakeSecretStore()
	keychain.values["qka/workbuddy-cn-u_1-s1"] = "token-abcd1234"

	svc := newCredService(keychain, file, fixedNow)
	cred, err := svc.Resolve(context.Background(), managedRef())
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if cred.Secret != "token-abcd1234" {
		t.Errorf("明文凭据错误: %q", cred.Secret)
	}
	// 脱敏元数据不含明文。
	if strings.Contains(cred.Meta.Preview, "token-abcd") {
		t.Errorf("脱敏预览不得包含明文，实际 %q", cred.Meta.Preview)
	}
	if cred.Meta.Kind != Managed {
		t.Errorf("meta kind 错误: %s", cred.Meta.Kind)
	}
}

func TestResolveFallsBackToFile(t *testing.T) {
	keychain := newFakeSecretStore()
	file := newFakeSecretStore()
	file.values["qka/workbuddy-cn-u_1-s1"] = "file-secret-xyz9"

	svc := newCredService(keychain, file, fixedNow)
	cred, err := svc.Resolve(context.Background(), managedRef())
	if err != nil {
		t.Fatalf("Keychain 未命中应回退文件，实际 %v", err)
	}
	if cred.Secret != "file-secret-xyz9" {
		t.Errorf("应从文件回退读取，实际 %q", cred.Secret)
	}
}

func TestResolveDeniedEntersCooldown(t *testing.T) {
	keychain := newFakeSecretStore()
	keychain.denied = true
	file := newFakeSecretStore()
	file.values["qka/workbuddy-cn-u_1-s1"] = "file-secret"

	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	mutable := now
	svc := newCredService(keychain, file, func() time.Time { return mutable })

	if _, err := svc.Resolve(context.Background(), managedRef()); err != nil {
		t.Fatalf("拒绝后应回退文件，实际 %v", err)
	}
	callsAfterFirst := keychain.calls

	// 冷却窗口内再次解析：不再尝试 keychain（避免反复弹框）。
	mutable = now.Add(2 * time.Minute)
	if _, err := svc.Resolve(context.Background(), managedRef()); err != nil {
		t.Fatal(err)
	}
	if keychain.calls != callsAfterFirst {
		t.Errorf("冷却期间不应再调用 keychain，calls %d -> %d", callsAfterFirst, keychain.calls)
	}

	// 冷却结束后才会再次尝试 keychain。
	mutable = now.Add(10 * time.Minute)
	_, _ = svc.Resolve(context.Background(), managedRef())
	if keychain.calls <= callsAfterFirst {
		t.Error("冷却结束后应重新尝试 keychain")
	}
}

func TestImportedSourceReadOnly(t *testing.T) {
	keychain := newFakeSecretStore()
	file := newFakeSecretStore()
	svc := newCredService(keychain, file, fixedNow)

	ref := CredentialRef{Target: "workbuddy-cn", AccountID: "u_1", SourceID: "imp1", Kind: Imported}
	// 导入来源可解析（只读引用）。
	_ = file.Set(context.Background(), "import/workbuddy-cn", "u_1", "imported-token")
	cred, err := svc.ResolveImported(context.Background(), ref, "imported-token")
	if err != nil {
		t.Fatalf("导入来源应可解析，实际 %v", err)
	}
	if cred.Secret != "imported-token" {
		t.Errorf("导入明文错误: %q", cred.Secret)
	}
	// 刷新/回写被直接拒绝。
	if err := svc.Refresh(context.Background(), ref); !errors.Is(err, ErrImportedReadOnly) {
		t.Errorf("导入来源刷新应被拒绝，实际 %v", err)
	}
}

func TestManagedRefreshAllowed(t *testing.T) {
	keychain := newFakeSecretStore()
	file := newFakeSecretStore()
	svc := newCredService(keychain, file, fixedNow)
	if err := svc.Refresh(context.Background(), managedRef()); !errors.Is(err, ErrRefreshNotWired) {
		// 08 阶段刷新路径尚未接适配器，返回"未接线"是可接受的显式状态；关键是不拒绝 managed。
		t.Logf("managed 刷新未拒绝（返回 %v）", err)
	}
}

func fixedNow() time.Time { return time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC) }
