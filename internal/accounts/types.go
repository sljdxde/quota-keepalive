// Package accounts 提供账号与登录态来源的统一管理：发现、导入、官方登录、
// 多来源优先级、逐账号启停、删除（仅 QKA 映射）与来源健康。
package accounts

// SourceKind 是来源类型。
type SourceKind string

const (
	// KindManaged 受管来源：可经官方机制刷新。
	KindManaged SourceKind = "managed"
	// KindImported 导入来源：只读、不回写。
	KindImported SourceKind = "imported"
)

// SourceHealth 是来源健康状态。
type SourceHealth string

const (
	HealthHealthy        SourceHealth = "healthy"
	HealthReauthRequired SourceHealth = "reauth_required"
	HealthDisabled       SourceHealth = "disabled"
)

// Source 是登录态来源。
type Source struct {
	ID        string       `json:"id"`
	Target    string       `json:"target"`
	AccountID string       `json:"account_id"`
	Kind      SourceKind   `json:"kind"`
	ReadOnly  bool         `json:"read_only"`
	Health    SourceHealth `json:"health"`
	Pinned    bool         `json:"pinned"`
}

// Account 是目标应用账号。
type Account struct {
	Target         string   `json:"target"`
	ID             string   `json:"id"`
	DisplayName    string   `json:"display_name"`
	MaskedIdentity string   `json:"masked_identity"`
	Enabled        bool     `json:"enabled"`
	Sources        []Source `json:"sources"`
}

// DiscoveredAccount 是适配器发现的账号（脱敏、无凭据）。
type DiscoveredAccount struct {
	ID             string
	DisplayName    string
	MaskedIdentity string
	SourceID       string
	SourceKind     SourceKind
}
