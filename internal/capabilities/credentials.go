package capabilities

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	serviceName = "qka"
	// keychainDeniedCooldown：授权被拒后短期内不再触发 Keychain，避免反复弹框。
	keychainDeniedCooldown = 5 * time.Minute
)

var (
	// ErrSecretNotFound：底层存储无该凭据。
	ErrSecretNotFound = errors.New("凭据未找到")
	// ErrAuthDenied：Keychain 授权被用户拒绝。
	ErrAuthDenied = errors.New("Keychain 授权被拒绝")
	// ErrImportedReadOnly：导入来源只读，拒绝刷新/回写。
	ErrImportedReadOnly = errors.New("导入来源只读，拒绝刷新/回写")
	// ErrRefreshNotWired：官方刷新路径尚未接适配器（08 阶段的显式状态）。
	ErrRefreshNotWired = errors.New("官方刷新路径尚未接线")
)

// SourceKind 是登录态来源类型。
type SourceKind string

const (
	// Managed 受管来源：仅官方登录创建，可按官方机制刷新。
	Managed SourceKind = "managed"
	// Imported 导入来源：永远只读。
	Imported SourceKind = "imported"
)

// CredentialRef 标识一个来源的凭据。
type CredentialRef struct {
	Target    string
	AccountID string
	SourceID  string
	Kind      SourceKind
}

// CredentialMeta 是可进日志/结果的脱敏元数据。
type CredentialMeta struct {
	Target    string     `json:"target"`
	AccountID string     `json:"account_id"`
	SourceID  string     `json:"source_id"`
	Kind      SourceKind `json:"kind"`
	Preview   string     `json:"preview"`
}

// Credential 是凭据解析结果：明文仅在内存、当次有效。
type Credential struct {
	Secret string
	Meta   CredentialMeta
}

// SecretStore 是凭据明文的底层存储（Keychain 或受保护文件）。
type SecretStore interface {
	Set(ctx context.Context, service, account, secret string) error
	Get(ctx context.Context, service, account string) (string, error)
	Delete(ctx context.Context, service, account string) error
}

// Clock 返回当前时间，便于注入。
type Clock func() time.Time

// CredentialService 提供凭据解析、持久化与刷新策略。
type CredentialService struct {
	keychain SecretStore
	file     SecretStore
	grant    Grant
	now      Clock

	mu                  sync.Mutex
	keychainDeniedUntil time.Time
}

// NewCredentialService 组装凭据服务；Keychain 优先、文件回退。
func NewCredentialService(keychain, file SecretStore, grant Grant, now Clock) *CredentialService {
	if now == nil {
		now = time.Now
	}
	return &CredentialService{keychain: keychain, file: file, grant: grant, now: now}
}

func (c *CredentialService) secretAccount(ref CredentialRef) string {
	return fmt.Sprintf("%s-%s-%s", ref.Target, ref.AccountID, ref.SourceID)
}

func maskSecret(secret string) string {
	if len(secret) <= 4 {
		return "****"
	}
	return "****" + secret[len(secret)-4:]
}

func (c *CredentialService) meta(ref CredentialRef, secret string) CredentialMeta {
	return CredentialMeta{
		Target: ref.Target, AccountID: ref.AccountID, SourceID: ref.SourceID,
		Kind: ref.Kind, Preview: maskSecret(secret),
	}
}

// Resolve 解析 managed 凭据：Keychain 优先，授权被拒进入冷却并回退文件。
func (c *CredentialService) Resolve(ctx context.Context, ref CredentialRef) (Credential, error) {
	if err := c.grant.Authorize("credentials.resolve"); err != nil {
		return Credential{}, err
	}
	if ref.Kind != Managed {
		return Credential{}, ErrImportedReadOnly
	}

	account := c.secretAccount(ref)
	now := c.now()

	c.mu.Lock()
	inCooldown := now.Before(c.keychainDeniedUntil)
	c.mu.Unlock()

	if !inCooldown {
		secret, err := c.keychain.Get(ctx, serviceName, account)
		if err == nil {
			return Credential{Secret: secret, Meta: c.meta(ref, secret)}, nil
		}
		if errors.Is(err, ErrAuthDenied) {
			c.mu.Lock()
			c.keychainDeniedUntil = now.Add(keychainDeniedCooldown)
			c.mu.Unlock()
		}
		// 未找到或被拒：继续回退文件。
	}

	secret, err := c.file.Get(ctx, serviceName, account)
	if err != nil {
		return Credential{}, err
	}
	return Credential{Secret: secret, Meta: c.meta(ref, secret)}, nil
}

// ResolveImported 为导入来源构造只读凭据；明文由调用方从目标应用存储读取，QKA 不写入。
func (c *CredentialService) ResolveImported(ctx context.Context, ref CredentialRef, secret string) (Credential, error) {
	if err := c.grant.Authorize("credentials.resolve"); err != nil {
		return Credential{}, err
	}
	if ref.Kind != Imported {
		return Credential{}, fmt.Errorf("导入来源必须标记 imported")
	}
	return Credential{Secret: secret, Meta: c.meta(ref, secret)}, nil
}

// Persist 保存 managed 凭据：Keychain 优先，失败回退受保护文件。
func (c *CredentialService) Persist(ctx context.Context, ref CredentialRef, secret string) error {
	if err := c.grant.Authorize("credentials.managed"); err != nil {
		return err
	}
	if ref.Kind != Managed {
		return ErrImportedReadOnly
	}
	account := c.secretAccount(ref)
	if err := c.keychain.Set(ctx, serviceName, account, secret); err == nil {
		return nil
	}
	return c.file.Set(ctx, serviceName, account, secret)
}

// Refresh 仅 managed 来源允许（官方刷新路径在后续适配器接线）；imported 直接拒绝。
func (c *CredentialService) Refresh(ctx context.Context, ref CredentialRef) error {
	if err := c.grant.Authorize("credentials.managed"); err != nil {
		return err
	}
	if ref.Kind == Imported {
		return ErrImportedReadOnly
	}
	return ErrRefreshNotWired
}
