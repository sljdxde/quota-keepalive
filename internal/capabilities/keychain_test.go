package capabilities

import (
	"context"
	"errors"
	"testing"
)

func runnerReturning(r execResult) commandRunner {
	return func(ctx context.Context, name string, args ...string) (execResult, error) {
		return r, nil
	}
}

func TestKeychainGetSuccess(t *testing.T) {
	k := NewKeychainStore().withRunner(runnerReturning(execResult{
		stdout: []byte("token-value\n"), exitCode: 0}))
	got, err := k.Get(context.Background(), "qka", "acct")
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	if got != "token-value" {
		t.Errorf("应去掉尾部换行并返回明文，实际 %q", got)
	}
}

func TestKeychainGetNotFound(t *testing.T) {
	k := NewKeychainStore().withRunner(runnerReturning(execResult{
		stderr:   []byte("security: SecKeychainSearchCopyNext: The specified item could not be found"),
		exitCode: 44}))
	if _, err := k.Get(context.Background(), "qka", "acct"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("退出码 44 应映射未找到，实际 %v", err)
	}
}

func TestKeychainGetAuthDenied(t *testing.T) {
	k := NewKeychainStore().withRunner(runnerReturning(execResult{
		stderr: []byte("User interaction is not allowed"), exitCode: 128}))
	if _, err := k.Get(context.Background(), "qka", "acct"); !errors.Is(err, ErrAuthDenied) {
		t.Fatalf("授权被拒应映射 ErrAuthDenied，实际 %v", err)
	}
}

func TestKeychainSetSuccess(t *testing.T) {
	k := NewKeychainStore().withRunner(runnerReturning(execResult{exitCode: 0}))
	if err := k.Set(context.Background(), "qka", "acct", "secret"); err != nil {
		t.Fatalf("Set 失败: %v", err)
	}
}

func TestKeychainSetAuthDenied(t *testing.T) {
	k := NewKeychainStore().withRunner(runnerReturning(execResult{
		stderr: []byte("User canceled the operation"), exitCode: 128}))
	if err := k.Set(context.Background(), "qka", "acct", "secret"); !errors.Is(err, ErrAuthDenied) {
		t.Fatalf("取消授权应映射 ErrAuthDenied，实际 %v", err)
	}
}

func TestKeychainDeleteNotFound(t *testing.T) {
	k := NewKeychainStore().withRunner(runnerReturning(execResult{exitCode: 44}))
	if err := k.Delete(context.Background(), "qka", "acct"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("删除不存在项应映射未找到，实际 %v", err)
	}
}
