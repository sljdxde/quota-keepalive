package adapterwiring

import (
	"encoding/json"

	"qka/internal/evidence"
)

// EvidenceFromCheckin 把适配器 checkin 结果映射为证据链（白名单字段）。
func EvidenceFromCheckin(raw json.RawMessage, endpoint string) evidence.Chain {
	var data struct {
		BalanceBefore int    `json:"balanceBefore"`
		BalanceAfter  int    `json:"balanceAfter"`
		Receipt       string `json:"receipt"`
	}
	_ = json.Unmarshal(raw, &data)

	return evidence.Chain{
		Observed: []evidence.ObservedFact{{
			Endpoint:      endpoint,
			StatusCode:    200,
			BalanceBefore: data.BalanceBefore,
			BalanceAfter:  data.BalanceAfter,
			Receipt:       data.Receipt,
		}},
	}
}
