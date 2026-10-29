package cli

import "testing"

func TestConfigDirDefaultsToDotQkaUnderHome(t *testing.T) {
	getenv := func(k string) string {
		if k == "HOME" {
			return "/Users/yu"
		}
		return ""
	}
	got := ResolveConfigDir(getenv)
	if got != "/Users/yu/.qka" {
		t.Errorf("期望默认 ~/.qka，实际 %q", got)
	}
}

func TestConfigDirOverrideByEnv(t *testing.T) {
	getenv := func(k string) string {
		switch k {
		case "HOME":
			return "/Users/yu"
		case "QKA_CONFIG_DIR":
			return "/tmp/custom-qka"
		}
		return ""
	}
	got := ResolveConfigDir(getenv)
	if got != "/tmp/custom-qka" {
		t.Errorf("期望 QKA_CONFIG_DIR 覆盖，实际 %q", got)
	}
}
