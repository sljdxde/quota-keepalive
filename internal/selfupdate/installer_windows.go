//go:build windows

package selfupdate

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const windowsInstallerScript = `param(
    [Parameter(Mandatory=$true)][string]$Package,
    [Parameter(Mandatory=$true)][string]$Executable,
    [Parameter(Mandatory=$true)][int]$ProcessId
)

$ErrorActionPreference = "Stop"
$LogPath = Join-Path (Split-Path -Parent $PSCommandPath) "qka-install-error.log"
$StagingRoot = $null

function Get-TargetProcesses([string]$Directory) {
    $Canonical = [IO.Path]::GetFullPath($Directory)
    return Get-Process -Name "QKA", "QKA-cli" -ErrorAction SilentlyContinue |
        Where-Object {
            try {
                $null -ne $_.Path -and
                [IO.Path]::GetFullPath((Split-Path -Parent $_.Path)) -eq $Canonical
            } catch {
                $false
            }
        }
}

function Stop-Update([string]$Message) {
    try {
        Add-Content -LiteralPath $LogPath -Value ((Get-Date).ToString("o") + " " + $Message)
    } catch {
    }
    exit 1
}

try {
    while (Get-Process -Id $ProcessId -ErrorAction SilentlyContinue) {
        Start-Sleep -Seconds 1
    }
    if (-not (Test-Path -LiteralPath $Package -PathType Leaf)) {
        throw "update package is missing"
    }
    if (-not (Test-Path -LiteralPath $Executable -PathType Leaf)) {
        throw "target executable is missing"
    }

    $TargetDir = Split-Path -Parent $Executable
    $ExecutableName = Split-Path -Leaf $Executable
    if ([string]::IsNullOrWhiteSpace($TargetDir) -or
        $TargetDir -eq [IO.Path]::GetPathRoot($TargetDir)) {
        throw "target directory is invalid"
    }
    $ParentDir = Split-Path -Parent $TargetDir
    if ([string]::IsNullOrWhiteSpace($ParentDir)) {
        throw "target parent directory is invalid"
    }
    $Deadline = (Get-Date).AddMinutes(2)
    while (Get-TargetProcesses $TargetDir) {
        if ((Get-Date) -gt $Deadline) {
            throw "QKA is still running"
        }
        Start-Sleep -Seconds 1
    }

    # Keep staging on the target volume so every managed item can be swapped by
    # a same-volume rename rather than a cross-volume copy.
    $StagingRoot = Join-Path $ParentDir (
        ".qka-update-" + [Guid]::NewGuid().ToString("N")
    )
    New-Item -ItemType Directory -Path $StagingRoot | Out-Null
    Expand-Archive -LiteralPath $Package -DestinationPath $StagingRoot -Force

    $Candidate = Get-ChildItem -LiteralPath $StagingRoot -Filter $ExecutableName -File -Recurse |
        Sort-Object FullName | Select-Object -First 1
    if ($null -eq $Candidate) {
        throw "update package does not contain $ExecutableName"
    }
    $SourceDir = Split-Path -Parent $Candidate.FullName
    $BackupDir = "$TargetDir.qka-old"
    $ManagedItems = @(
        "QKA.exe",
        "QKA-cli.exe",
        "README.txt",
        "bun.exe",
        "adapters",
        "provider-plugins"
    )
    foreach ($Item in $ManagedItems) {
        if (-not (Test-Path -LiteralPath (Join-Path $SourceDir $Item))) {
            throw "update package is missing managed item $Item"
        }
    }
    $InstalledItems = @()

    try {
        if (Test-Path -LiteralPath $BackupDir) {
            Remove-Item -LiteralPath $BackupDir -Recurse -Force
        }
        New-Item -ItemType Directory -Path $BackupDir | Out-Null

        # Only QKA-owned package items are moved. Unrelated files in the same
        # folder remain untouched, including on the next update.
        foreach ($Item in $ManagedItems) {
            $TargetItem = Join-Path $TargetDir $Item
            if (Test-Path -LiteralPath $TargetItem) {
                Move-Item -LiteralPath $TargetItem -Destination $BackupDir
            }
        }
        foreach ($Item in $ManagedItems) {
            $SourceItem = Join-Path $SourceDir $Item
            $TargetItem = Join-Path $TargetDir $Item
            Move-Item -LiteralPath $SourceItem -Destination $TargetItem
            $InstalledItems += $TargetItem
        }

        $NewExecutable = Join-Path $TargetDir $ExecutableName
        $NewProcess = Start-Process -FilePath $NewExecutable -WorkingDirectory $TargetDir -PassThru
        Start-Sleep -Seconds 5
        if ($NewProcess.HasExited) {
            throw "updated QKA exited during startup"
        }
    } catch {
        $Failure = $_.Exception.Message
        foreach ($TargetItem in $InstalledItems) {
            if (Test-Path -LiteralPath $TargetItem) {
                Remove-Item -LiteralPath $TargetItem -Recurse -Force
            }
        }
        foreach ($Item in $ManagedItems) {
            $BackupItem = Join-Path $BackupDir $Item
            if (Test-Path -LiteralPath $BackupItem) {
                Move-Item -LiteralPath $BackupItem -Destination $TargetDir -Force
            }
        }
        $OldExecutable = Join-Path $TargetDir $ExecutableName
        if (Test-Path -LiteralPath $OldExecutable -PathType Leaf) {
            Start-Process -FilePath $OldExecutable -WorkingDirectory $TargetDir | Out-Null
        }
        throw $Failure
    }
} catch {
    Stop-Update $_.Exception.Message
} finally {
    if ($null -ne $StagingRoot -and (Test-Path -LiteralPath $StagingRoot)) {
        Remove-Item -LiteralPath $StagingRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
}
`

func startInstaller(ctx context.Context, executable, packagePath string) error {
	if executable == "" {
		return errors.New("selfupdate: executable path is unavailable")
	}
	if !strings.EqualFold(filepath.Ext(packagePath), ".zip") {
		return errors.New("selfupdate: Windows updates require a ZIP package")
	}
	scriptPath := filepath.Join(filepath.Dir(packagePath), "qka-install.ps1")
	if err := os.WriteFile(scriptPath, []byte(windowsInstallerScript), 0o700); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "powershell.exe",
		"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", scriptPath, packagePath, executable, strconv.Itoa(updateProcessID()),
	)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func (s *Service) installer() (string, error) {
	exe := s.Executable
	if exe == nil {
		exe = os.Executable
	}
	return exe()
}
