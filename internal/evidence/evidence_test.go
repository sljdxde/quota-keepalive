package evidence

import (
	"testing"
	"time"
)

func beijing(hour int) time.Time {
	return time.Date(2026, 10, 29, hour, 0, 0, 0,
		time.FixedZone("Asia/Shanghai", 8*3600))
}

func TestChainSufficientWithBalanceIncrease(t *testing.T) {
	chain := Chain{
		Observed: []ObservedFact{
			{Endpoint: "https://api.example.com/checkin", StatusCode: 200,
				BalanceBefore: 100, BalanceAfter: 300},
		},
	}
	if !chain.Sufficient() {
		t.Error("观察到余额增加应足以确认")
	}
}

func TestChainSufficientWithExplicitReceipt(t *testing.T) {
	chain := Chain{
		Observed: []ObservedFact{
			{StatusCode: 200, Receipt: "receipt-abc"},
		},
	}
	if !chain.Sufficient() {
		t.Error("上游明确回执应足以确认")
	}
}

func TestChainInsufficientWithoutBalanceOrReceipt(t *testing.T) {
	chain := Chain{
		Observed: []ObservedFact{{StatusCode: 200}},
	}
	if chain.Sufficient() {
		t.Error("仅有领取成功、无余额/回执不应确认")
	}
}

func TestDecideMorningAlwaysProvisional(t *testing.T) {
	// 即使证据充分，午前也停在 provisional，待午后复核。
	chain := Chain{Observed: []ObservedFact{
		{BalanceBefore: 0, BalanceAfter: 200},
	}}
	d := Decide(beijing(10), chain)
	if d.State != StateProvisional {
		t.Errorf("午前成功应 provisional，实际 %s", d.State)
	}
	if d.Message != PendingReviewMessage {
		t.Errorf("文案应为待复核，实际 %q", d.Message)
	}
}

func TestDecideAfternoonConfirmedWhenSufficient(t *testing.T) {
	chain := Chain{Observed: []ObservedFact{
		{BalanceBefore: 0, BalanceAfter: 200},
	}}
	d := Decide(beijing(14), chain)
	if d.State != StateConfirmed {
		t.Errorf("午后证据充分应 confirmed，实际 %s", d.State)
	}
}

func TestDecideAfternoonProvisionalWhenInsufficient(t *testing.T) {
	chain := Chain{Observed: []ObservedFact{{StatusCode: 200}}}
	d := Decide(beijing(14), chain)
	if d.State != StateProvisional {
		t.Errorf("午后缺证据应仍 provisional，实际 %s", d.State)
	}
	if d.Message != PendingReviewMessage {
		t.Errorf("缺证据文案应待复核，实际 %q", d.Message)
	}
}

func TestEvidenceContainsNoSensitiveFields(t *testing.T) {
	chain := Chain{
		Observed: []ObservedFact{
			{Endpoint: "https://api.example.com/x?token=secret", StatusCode: 200},
		},
	}
	sanitized := Sanitize(chain)
	out := mustJSON(sanitized)
	if containsAny(out, "secret", "cookie", "authorization", "set-cookie") {
		t.Errorf("证据不得含凭据/敏感参数，实际 %s", out)
	}
}

func TestInvalidEndpointIsRedacted(t *testing.T) {
	chain := Sanitize(Chain{Observed: []ObservedFact{{Endpoint: "https://example.com/%zz"}}})
	if got := chain.Observed[0].Endpoint; got != "REDACTED" {
		t.Fatalf("无法解析的端点应完全脱敏，实际 %q", got)
	}
}
