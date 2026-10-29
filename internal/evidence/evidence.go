// Package evidence 承载到账证据链与 provisional/confirmed 确认规则。
// 证据字段为白名单：仅含脱敏端点、状态码、时间戳、前后余额与回执，不含凭据/完整头/cookie/原始响应。
package evidence

import (
	"net/url"
	"strings"
	"time"
)

// State 是确认决策的状态。
type State string

const (
	// StateProvisional 已领取、待复核。
	StateProvisional State = "signed_provisional"
	// StateConfirmed 证据充分、已到账。
	StateConfirmed State = "signed_confirmed"
)

// PendingReviewMessage 是缺证据时的统一文案（既不写成没发生，也不写成已到账）。
const PendingReviewMessage = "已领取，待复核"

// ObservedFact 是直接观察到的事实（白名单字段）。
type ObservedFact struct {
	Endpoint      string `json:"endpoint"`
	StatusCode    int    `json:"status_code"`
	At            string `json:"at,omitempty"`
	BalanceBefore int    `json:"balance_before,omitempty"`
	BalanceAfter  int    `json:"balance_after,omitempty"`
	Receipt       string `json:"receipt,omitempty"`
}

// InferredFact 是推断事实（带置信度与依据）。
type InferredFact struct {
	Claim      string  `json:"claim"`
	Confidence float64 `json:"confidence"`
	Basis      string  `json:"basis"`
}

// Chain 是一条证据链。
type Chain struct {
	Observed []ObservedFact `json:"observed"`
	Inferred []InferredFact `json:"inferred"`
	Unknown  []string       `json:"unknown"`
}

// Sufficient 报告证据是否足以确认：观察到余额增加，或上游明确回执。
func (c Chain) Sufficient() bool {
	for _, o := range c.Observed {
		if o.BalanceAfter > o.BalanceBefore {
			return true
		}
		if strings.TrimSpace(o.Receipt) != "" {
			return true
		}
	}
	return false
}

// Decision 是确认决策。
type Decision struct {
	State   State
	Message string
}

// Decide 依据时间与证据链决定状态：
// 午前成功恒为 provisional（待午后复核）；午后证据充分才 confirmed，否则仍 provisional。
func Decide(now time.Time, chain Chain) Decision {
	hour := now.In(time.FixedZone("Asia/Shanghai", 8*3600)).Hour()
	if hour < 12 {
		return Decision{State: StateProvisional, Message: PendingReviewMessage}
	}
	if chain.Sufficient() {
		return Decision{State: StateConfirmed, Message: "已到账"}
	}
	return Decision{State: StateProvisional, Message: PendingReviewMessage}
}

var sensitiveParams = []string{"token", "key", "secret", "sign", "auth", "password"}

// Sanitize 脱敏证据端点（query 敏感参数）；结构本身已是白名单，不含凭据/头/cookie/body。
func Sanitize(chain Chain) Chain {
	for i, o := range chain.Observed {
		chain.Observed[i].Endpoint = redactEndpoint(o.Endpoint)
	}
	return chain
}

func redactEndpoint(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "REDACTED"
	}
	q := u.Query()
	for k := range q {
		lower := strings.ToLower(k)
		for _, s := range sensitiveParams {
			if strings.Contains(lower, s) {
				q.Set(k, "REDACTED")
				break
			}
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}
