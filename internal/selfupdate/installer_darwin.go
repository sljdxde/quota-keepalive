//go:build darwin

package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const macInstallerScript = `#!/bin/sh
set -eu
PKG="$1"
APP="$2"
PID="$3"
while kill -0 "$PID" 2>/dev/null; do sleep 1; done
MOUNT=""
cleanup() {
  if [ -n "$MOUNT" ]; then hdiutil detach "$MOUNT" -quiet >/dev/null 2>&1 || true; fi
}
trap cleanup EXIT
if [ "${PKG##*.}" = "dmg" ]; then
  MOUNT=$(hdiutil attach "$PKG" -nobrowse -readonly | tail -1 | awk -F '\t' '{print $NF}')
  SRC="$MOUNT/QKA.app"
else
  SRC="$PKG"
fi
if [ ! -x "$SRC/Contents/MacOS/qka" ]; then exit 1; fi
if command -v plutil >/dev/null 2>&1; then
  BUNDLE_ID=$(plutil -extract CFBundleIdentifier raw -o - \
    "$SRC/Contents/Info.plist" 2>/dev/null || true)
  if [ "$BUNDLE_ID" != "com.qka.agent" ]; then exit 1; fi
fi
if ! codesign --verify --deep --strict "$SRC" >/dev/null 2>&1; then exit 1; fi
OLD="$APP.qka-old"
NEW="$APP.qka-new"
if [ -e "$OLD" ]; then OLD="$OLD.$(date +%s).$$"; fi
if [ -e "$NEW" ]; then NEW="$NEW.$(date +%s).$$"; fi
mv "$APP" "$OLD"
if ditto "$SRC" "$NEW" && mv "$NEW" "$APP"; then
  open "$APP" >/dev/null 2>&1 || true
  sleep 5
  if pgrep -f "$APP/Contents/MacOS/qka" >/dev/null 2>&1; then
    rm -rf "$OLD"
    rm -f "$0"
    exit 0
  fi
fi
if [ -e "$APP" ]; then mv "$APP" "$APP.qka-failed.$(date +%s).$$" || true; fi
if [ -e "$NEW" ]; then rm -rf "$NEW"; fi
mv "$OLD" "$APP"
open "$APP" >/dev/null 2>&1 || true
exit 1
`

func startInstaller(ctx context.Context, appBundle, packagePath string) error {
	if appBundle == "" {
		return errors.New("selfupdate: application bundle is unavailable")
	}
	scriptPath := filepath.Join(filepath.Dir(packagePath), "qka-install.sh")
	if err := os.WriteFile(scriptPath, []byte(macInstallerScript), 0o700); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "/bin/sh", scriptPath, packagePath, appBundle,
		strconv.Itoa(updateProcessID()))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func applicationBundle(executable string) (string, error) {
	clean := filepath.Clean(executable)
	index := strings.Index(clean, ".app/Contents/MacOS/")
	if index < 0 {
		return "", errors.New("selfupdate: 仅 .app 安装版支持无感更新")
	}
	bundle := clean[:index+len(".app")]
	if info, err := os.Stat(bundle); err != nil || !info.IsDir() {
		return "", fmt.Errorf("selfupdate: application bundle is unavailable")
	}
	return bundle, nil
}

func (s *Service) installer() (string, error) {
	exe := s.Executable
	if exe == nil {
		exe = os.Executable
	}
	path, err := exe()
	if err != nil {
		return "", err
	}
	return applicationBundle(path)
}
