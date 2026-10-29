package doctor

import (
	"testing"
	"time"
)

func TestAllHealthyReport(t *testing.T) {
	now := time.Now()
	report := Run(now, Inputs{
		Version: "1.0.0", EndpointReachable: true, PairingPresent: true,
		CredentialReadable: true, LastInvocation: now,
		AdapterEffectsComplete: true, FilePermissionsOK: true,
	})

	// 除 host_internal_plan 恒为 not_observable 外，其余均应 ok。
	for _, c := range report.Checks {
		if c.Name == "host_internal_plan" {
			if c.Verdict != VerdictWarn {
				t.Errorf("宿主内部计划应恒 not_observable，实际 %s", c.Verdict)
			}
			if c.Fix == "" {
				t.Error("应附宿主侧核验指引")
			}
			continue
		}
		if c.Verdict != VerdictOK {
			t.Errorf("检查项 %s 应 ok，实际 %s（%s）", c.Name, c.Verdict, c.Detail)
		}
	}
}

func TestEndpointFailureIncludesOccupant(t *testing.T) {
	report := Run(time.Now(), Inputs{
		EndpointReachable: false, PortOccupant: "PID 502 other-app",
	})
	var endpoint *Check
	for i := range report.Checks {
		if report.Checks[i].Name == "endpoint" {
			endpoint = &report.Checks[i]
		}
	}
	if endpoint.Verdict != VerdictFail {
		t.Fatalf("端点不可达应 fail，实际 %s", endpoint.Verdict)
	}
	if !contains(endpoint.Detail, "PID 502") {
		t.Errorf("应识别端口占用者，实际 %s", endpoint.Detail)
	}
	if !contains(endpoint.Fix, "不会自动杀进程") {
		t.Error("修复指引应说明不杀进程")
	}
}

func TestKeychainCooldownWarns(t *testing.T) {
	report := Run(time.Now(), Inputs{
		EndpointReachable: true, CredentialDeniedCooldown: true,
	})
	cred := findCheck(report, "credentials")
	if cred.Verdict != VerdictWarn {
		t.Errorf("Keychain 冷却应 warn，实际 %s", cred.Verdict)
	}
}

func TestMissingEffectsWarnsConservative(t *testing.T) {
	report := Run(time.Now(), Inputs{
		EndpointReachable: true, CredentialReadable: true,
		AdapterEffectsComplete: false,
	})
	eff := findCheck(report, "adapter_effects")
	if eff.Verdict != VerdictWarn {
		t.Errorf("效应缺失应 warn，实际 %s", eff.Verdict)
	}
	if !contains(eff.Detail, "保守") {
		t.Error("应说明按保守值处理")
	}
}

func TestStaleInvocationWarns(t *testing.T) {
	now := time.Now()
	report := Run(now, Inputs{
		EndpointReachable: true, CredentialReadable: true,
		LastInvocation: now.Add(-25 * time.Hour),
	})
	fr := findCheck(report, "freshness")
	if fr.Verdict != VerdictWarn {
		t.Errorf("超过 24h 应 warn，实际 %s", fr.Verdict)
	}
}

func findCheck(r Report, name string) Check {
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	return Check{}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
