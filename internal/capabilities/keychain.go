package capabilities

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"unicode/utf8"
)

// execResult 是一次外部命令的结果（含退出码，便于测试模拟）。
type execResult struct {
	stdout   []byte
	stderr   []byte
	exitCode int
}

// commandRunner 执行一条外部命令；生产用 realRunner，测试用假件。
type commandRunner func(ctx context.Context, name string, args ...string) (execResult, error)

func realRunner(ctx context.Context, name string, args ...string) (execResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	r := execResult{stdout: stdout.Bytes(), stderr: stderr.Bytes()}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		r.exitCode = exitErr.ExitCode()
		return r, nil
	}
	if err != nil {
		return r, err
	}
	return r, nil
}

// KeychainStore 用 macOS security CLI 访问通用密码项（无 CGO）。
type KeychainStore struct {
	run commandRunner
}

// NewKeychainStore 组装生产 Keychain 后端。
func NewKeychainStore() *KeychainStore {
	return &KeychainStore{run: realRunner}
}

func (k *KeychainStore) withRunner(r commandRunner) *KeychainStore {
	k.run = r
	return k
}

func (k *KeychainStore) Set(ctx context.Context, service, account, secret string) error {
	r, err := k.run(ctx, "security",
		"add-generic-password", "-U", "-a", account, "-s", service, "-w", secret)
	if err != nil {
		return err
	}
	if r.exitCode == 0 {
		return nil
	}
	if isAuthDenied(r) {
		return ErrAuthDenied
	}
	return fmt.Errorf("keychain 写入失败: %s", strings.TrimSpace(string(r.stderr)))
}

func (k *KeychainStore) Get(ctx context.Context, service, account string) (string, error) {
	r, err := k.run(ctx, "security",
		"find-generic-password", "-a", account, "-s", service, "-w")
	if err != nil {
		return "", err
	}
	if r.exitCode == 0 {
		return decodeKeychainPassword(r.stdout), nil
	}
	if r.exitCode == 44 {
		return "", ErrSecretNotFound
	}
	if isAuthDenied(r) {
		return "", ErrAuthDenied
	}
	return "", fmt.Errorf("keychain 读取失败: %s", strings.TrimSpace(string(r.stderr)))
}

// decodeKeychainPassword handles the security CLI's hexadecimal output format.
// `security -w` prints hex when the stored secret contains control characters,
// which is common for the pretty-printed JSON saved by provider plugins.
func decodeKeychainPassword(stdout []byte) string {
	value := strings.TrimRight(string(stdout), "\r\n")
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) == 0 || !utf8.Valid(decoded) {
		return value
	}
	trimmed := bytes.TrimSpace(decoded)
	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
		return value
	}
	return string(decoded)
}

func (k *KeychainStore) Delete(ctx context.Context, service, account string) error {
	r, err := k.run(ctx, "security",
		"delete-generic-password", "-a", account, "-s", service)
	if err != nil {
		return err
	}
	if r.exitCode == 0 {
		return nil
	}
	if r.exitCode == 44 {
		return ErrSecretNotFound
	}
	if isAuthDenied(r) {
		return ErrAuthDenied
	}
	return fmt.Errorf("keychain 删除失败: %s", strings.TrimSpace(string(r.stderr)))
}

// isAuthDenied 识别用户取消/拒绝授权的输出。
func isAuthDenied(r execResult) bool {
	if r.exitCode == 128 {
		return true
	}
	combined := strings.ToLower(string(r.stderr) + " " + string(r.stdout))
	return strings.Contains(combined, "user canceled") ||
		strings.Contains(combined, "user interaction is not allowed")
}
