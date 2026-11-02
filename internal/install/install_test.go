package install

import (
	"crypto/sha256"
	"strings"
	"testing"
)

func TestFiveProfilesComplete(t *testing.T) {
	profiles := Profiles()
	if len(profiles) != 5 {
		t.Fatalf("应为五个宿主提供配置档案，实际 %d", len(profiles))
	}
	for _, p := range profiles {
		for _, field := range []string{p.ConfigLocation, p.Transport, p.CredentialWrite, p.ScheduleGuide, p.Uninstall} {
			if field == "" {
				t.Errorf("宿主 %s 档案字段不完整（位置/传输/凭据/计划/卸载）", p.HostID)
			}
		}
	}
}

func TestPreviewReadOnlyPlan(t *testing.T) {
	plan, err := Preview("workbuddy")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{plan.DownloadURL, plan.InstallPath, plan.HostConfigChange,
		plan.BackupPath, plan.Verify, plan.Undo} {
		if want == "" {
			t.Error("预演应包含下载物/路径/将改配置/备份/验证/撤销")
		}
	}
	if len(plan.Steps) != 7 {
		t.Errorf("预演应列出完整步骤，实际 %d", len(plan.Steps))
	}
}

func TestPreviewUnsupportedHost(t *testing.T) {
	if _, err := Preview("unknown-ide"); err != ErrUnsupportedHost {
		t.Fatalf("不支持 MCP 的宿主应如实标注，实际 %v", err)
	}
}

func TestPreviewRejectsKnownHostWithoutMCP(t *testing.T) {
	old := profiles
	t.Cleanup(func() { profiles = old })
	profiles = append([]Profile(nil), old...)
	profiles = append(profiles, Profile{HostID: "manual-only", MCPSupported: false})
	if _, err := Preview("manual-only"); err != ErrUnsupportedHost {
		t.Fatalf("已知但不支持 MCP 的宿主应拒绝，实际 %v", err)
	}
}

func TestVerifyDownload(t *testing.T) {
	data := []byte("qka-binary")
	sum := sha256.Sum256(data)
	if err := VerifyDownload(data, sum[:]); err != nil {
		t.Fatalf("哈希一致应通过，实际 %v", err)
	}
	if err := VerifyDownload([]byte("tampered"), sum[:]); err != ErrDownloadChecksum {
		t.Fatalf("哈希不一致应失败，实际 %v", err)
	}
}

func TestScheduleGuideIsObservational(t *testing.T) {
	// 无内置持久计划的宿主，指引应明确用系统调度且仅观测，不宣称已启用。
	for _, p := range Profiles() {
		if strings.Contains(p.HostID, "codex") || strings.Contains(p.HostID, "claude") ||
			strings.Contains(p.HostID, "opencode") {
			if !strings.Contains(p.ScheduleGuide, "仅观测") {
				t.Errorf("宿主 %s 无内置计划，应标注仅观测核验", p.HostID)
			}
		}
	}
}
