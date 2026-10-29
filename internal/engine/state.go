package engine

import "time"

// Operation 是每日操作类型：签到与保活是两套独立状态机。
type Operation string

const (
	Checkin   Operation = "checkin"
	Keepalive Operation = "keepalive"
)

// State 是每日操作的机器可读状态。
type State string

const (
	Pending           State = "pending"
	Running           State = "running"
	SignedProvisional State = "signed_provisional"
	SignedConfirmed   State = "signed_confirmed"
	Already           State = "already"
	None              State = "none"
	FailedRetryable   State = "failed_retryable"
	FailedFinal       State = "failed_final"
	Completed         State = "completed"
)

// IsTerminal 报告是否为抑制同日再次执行的终态。
func (s State) IsTerminal() bool {
	switch s {
	case SignedProvisional, SignedConfirmed, Already, Completed:
		return true
	}
	return false
}

// ResultKind 是外部动作的结果类别（适配器无关）。
type ResultKind string

const (
	RSuccess   ResultKind = "success"
	RAlready   ResultKind = "already"
	RNone      ResultKind = "none"
	RRetryable ResultKind = "retryable"
	RFinal     ResultKind = "final"
)

// ExternalResult 是一次目标侧动作的结果。
type ExternalResult struct {
	Kind    ResultKind
	Message string
}

// Transition 是状态机纯函数：给定操作、结果类别与时间，返回新状态。
// 签到：午前成功→signed_provisional，午后成功→signed_confirmed；
// 保活：成功→completed；其余按结果类别映射。
func Transition(op Operation, kind ResultKind, now time.Time) State {
	if op == Checkin {
		switch kind {
		case RSuccess:
			if IsBeforeBeijingNoon(now) {
				return SignedProvisional
			}
			return SignedConfirmed
		case RAlready:
			return Already
		case RNone:
			return None
		case RRetryable:
			return FailedRetryable
		case RFinal:
			return FailedFinal
		}
	}

	if op == Keepalive {
		switch kind {
		case RSuccess:
			return Completed
		case RRetryable:
			return FailedRetryable
		case RFinal:
			return FailedFinal
		}
	}
	return Pending
}
