// Package doctor 提供机读诊断（doctor_run）：固定输出 check/verdict/detail/fix，
// 覆盖安装、端点、配对、凭据、新鲜度、适配器效应与文件权限。
package doctor

import "time"

// Verdict 是单项检查结论。
type Verdict string

const (
	// VerdictOK 正常。
	VerdictOK Verdict = "ok"
	// VerdictWarn 需关注但不阻断。
	VerdictWarn Verdict = "warn"
	// VerdictFail 需修复。
	VerdictFail Verdict = "fail"
)

// Check 是一项稳定输出的检查。
type Check struct {
	Name    string  `json:"check"`
	Verdict Verdict `json:"verdict"`
	Detail  string  `json:"detail"`
	Fix     string  `json:"fix"`
}

// Inputs 是诊断所需的观测输入（由各能力收集后注入）。
type Inputs struct {
	Version string
	// EndpointReachable 表示 loopback 端点可连通。
	EndpointReachable bool
	// PortOccupant 是端点不可达时占用默认端口的进程描述（可空）。
	PortOccupant string
	// PairingPresent 表示存在宿主配对。
	PairingPresent bool
	// CredentialReadable 表示凭据可读取。
	CredentialReadable bool
	// CredentialDeniedCooldown 表示 Keychain 曾拒绝且处于冷却。
	CredentialDeniedCooldown bool
	// LastInvocation 是最近一次调用时间（零值表示从未）。
	LastInvocation time.Time
	// AdapterEffectsComplete 表示适配器效应元数据完整。
	AdapterEffectsComplete bool
	// FilePermissionsOK 表示状态文件权限正确（0600/0700）。
	FilePermissionsOK bool
}

// Report 是诊断报告。
type Report struct {
	Checks []Check `json:"checks"`
}

// staleThreshold 是最近调用新鲜度阈值。
const staleThreshold = 24 * time.Hour

// Run 基于观测输入生成稳定的诊断报告。
func Run(now time.Time, in Inputs) Report {
	checks := []Check{
		{
			Name:    "install",
			Verdict: VerdictOK,
			Detail:  "qka 版本 " + in.Version,
		},
	}

	// 端点健康与端口占用者。
	if in.EndpointReachable {
		checks = append(checks, Check{Name: "endpoint", Verdict: VerdictOK, Detail: "loopback 端点可连通"})
	} else {
		detail := "loopback 端点不可达"
		if in.PortOccupant != "" {
			detail += "，端口占用者: " + in.PortOccupant
		}
		checks = append(checks, Check{
			Name: "endpoint", Verdict: VerdictFail, Detail: detail,
			Fix: "重启 qka 或为占用端口的程序让路；不会自动杀进程",
		})
	}

	// 宿主配对。
	if in.PairingPresent {
		checks = append(checks, Check{Name: "pairing", Verdict: VerdictOK, Detail: "存在宿主配对"})
	} else {
		checks = append(checks, Check{
			Name: "pairing", Verdict: VerdictWarn, Detail: "未发现宿主配对",
			Fix: "在宿主应用中重新完成配对",
		})
	}

	// 凭据可读性（含 Keychain 拒绝冷却）。
	switch {
	case in.CredentialReadable:
		checks = append(checks, Check{Name: "credentials", Verdict: VerdictOK, Detail: "凭据可读取"})
	case in.CredentialDeniedCooldown:
		checks = append(checks, Check{
			Name: "credentials", Verdict: VerdictWarn,
			Detail: "Keychain 访问曾被拒绝，处于冷却期",
			Fix:    "稍后重试或在系统提示中允许 qka 访问钥匙串",
		})
	default:
		checks = append(checks, Check{
			Name: "credentials", Verdict: VerdictFail, Detail: "凭据不可读取",
			Fix: "重新登录或检查钥匙串权限",
		})
	}

	// 最近调用新鲜度。
	if in.LastInvocation.IsZero() {
		checks = append(checks, Check{Name: "freshness", Verdict: VerdictWarn, Detail: "从未观察到调用",
			Fix: "手动执行一次以确认链路"})
	} else if now.Sub(in.LastInvocation) > staleThreshold {
		checks = append(checks, Check{
			Name: "freshness", Verdict: VerdictWarn,
			Detail: "最近调用已超过 24 小时", Fix: "手动执行一次并检查宿主调度",
		})
	} else {
		checks = append(checks, Check{Name: "freshness", Verdict: VerdictOK, Detail: "最近调用新鲜"})
	}

	// 适配器版本/效应完整性（缺失按保守值并告警）。
	if in.AdapterEffectsComplete {
		checks = append(checks, Check{Name: "adapter_effects", Verdict: VerdictOK, Detail: "适配器效应元数据完整"})
	} else {
		checks = append(checks, Check{
			Name: "adapter_effects", Verdict: VerdictWarn,
			Detail: "效应元数据缺失，已按最保守值处理并告警",
			Fix:    "更新或重新安装适配器，切勿在效应未知时访问凭据",
		})
	}

	// 状态文件权限。
	if in.FilePermissionsOK {
		checks = append(checks, Check{Name: "file_permissions", Verdict: VerdictOK, Detail: "状态文件权限正确"})
	} else {
		checks = append(checks, Check{
			Name: "file_permissions", Verdict: VerdictFail,
			Detail: "存在权限过宽的状态文件", Fix: "修正为 0600/0700 或删除后重建",
		})
	}

	// 宿主内部计划恒为 not_observable，附宿主侧核验指引。
	checks = append(checks, Check{
		Name: "host_internal_plan", Verdict: VerdictWarn,
		Detail: "宿主内部计划对 qka 不可观察（not_observable）",
		Fix:    "在宿主侧核验：检查宿主的调度/配额页或官方控制台",
	})

	return Report{Checks: checks}
}
