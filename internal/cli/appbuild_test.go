package cli

import (
	"testing"

	"qka/internal/workbuddy"
)

func TestProductionWorkBuddyTargetUsesOfficialOriginAndAllowedHost(t *testing.T) {
	baseURLs := productionBaseURLs()
	if got := baseURLs["workbuddy-cn"]; got != workbuddy.DefaultBaseURL {
		t.Fatalf("安装版 WorkBuddy CN 应使用官方 API，实际 %q", got)
	}

	var allowed bool
	for _, host := range productionAllowedHosts() {
		if host == "copilot.tencent.com" {
			allowed = true
		}
	}
	if !allowed {
		t.Fatalf("WorkBuddy CN 官方主机必须在生产白名单中: %v", productionAllowedHosts())
	}
}
