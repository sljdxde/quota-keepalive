//go:build windows

package selfupdate

import (
	"context"
	"strings"
	"testing"
)

func TestWindowsInstallerScriptUsesPackageDirectoryTransaction(t *testing.T) {
	for _, required := range []string{
		"Expand-Archive",
		"Move-Item -LiteralPath $TargetItem -Destination $BackupDir",
		"Move-Item -LiteralPath $SourceItem -Destination $TargetItem",
		"Only QKA-owned package items are moved",
		"Get-TargetProcesses",
		".qka-old",
		"updated QKA exited during startup",
		"Start-Process -FilePath $OldExecutable",
	} {
		if !strings.Contains(windowsInstallerScript, required) {
			t.Errorf("installer script missing %q", required)
		}
	}
	if strings.Contains(windowsInstallerScript, "copy /Y") {
		t.Fatal("installer must not overwrite the executable with the ZIP directly")
	}
}

func TestWindowsInstallerRejectsNonZipBeforeLaunch(t *testing.T) {
	if err := startInstaller(context.Background(), `C:\QKA\QKA.exe`, `C:\updates\QKA.dmg`); err == nil {
		t.Fatal("non-ZIP Windows update must be rejected")
	}
}
