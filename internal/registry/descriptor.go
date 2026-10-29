package registry

import "qka/internal/adapterhost"

// UnavailableReason 是机器可读的不可用原因。
type UnavailableReason string

const (
	// ReasonNotSignedIn 未登录（无来源）。
	ReasonNotSignedIn UnavailableReason = "not_signed_in"
	// ReasonReauthRequired 登录态失效，需重登。
	ReasonReauthRequired UnavailableReason = "reauth_required"
	// ReasonPlatformUnsupported 当前平台不支持。
	ReasonPlatformUnsupported UnavailableReason = "platform_unsupported"
	// ReasonAdapterDisabled 适配器/账号被停用。
	ReasonAdapterDisabled UnavailableReason = "adapter_disabled"
	// ReasonSignatureInvalid 适配器验签失败。
	ReasonSignatureInvalid UnavailableReason = "signature_invalid"
)

// SourceView 是自描述中的来源视图。
type SourceView struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	ReadOnly bool   `json:"read_only"`
	Health   string `json:"health"`
	Pinned   bool   `json:"pinned"`
}

// AccountView 是自描述中的账号视图。
type AccountView struct {
	ID             string       `json:"id"`
	DisplayName    string       `json:"display_name"`
	MaskedIdentity string       `json:"masked_identity"`
	Enabled        bool         `json:"enabled"`
	Sources        []SourceView `json:"sources"`
}

// ActionView 是自描述中的操作及其效应声明。
type ActionView struct {
	Op      string              `json:"op"`
	Effects adapterhost.Effects `json:"effects"`
}

// AgentDescriptor 是目标应用的自描述对象。
type AgentDescriptor struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Version           string            `json:"version"`
	Capabilities      []string          `json:"capabilities"`
	Needs             []string          `json:"needs"`
	Actions           []ActionView      `json:"actions"`
	Enabled           bool              `json:"enabled"`
	Available         bool              `json:"available"`
	UnavailableReason UnavailableReason `json:"unavailable_reason,omitempty"`
	// ScheduleOwner 是固定职责说明：调度由 host 负责。
	ScheduleOwner string        `json:"schedule_owner"`
	Accounts      []AccountView `json:"accounts"`
	// LastInvocationAt 仅为观测，不证明未来计划。
	LastInvocationAt string `json:"last_invocation_at,omitempty"`
}
