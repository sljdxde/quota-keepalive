//go:build darwin

package autostart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlistContents(t *testing.T) {
	data := Plist("com.qka.agent", "/Applications/QKA & Tools.app/Contents/MacOS/qka")
	s := string(data)
	for _, want := range []string{
		"<string>com.qka.agent</string>",
		"QKA &amp; Tools.app",
		"<string>tray</string>",
		"<key>RunAtLoad</key>",
		"<key>AbandonProcessGroup</key>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("plist 应含 %s", want)
		}
	}
}

func TestPlistNamesQKABundle(t *testing.T) {
	home := t.TempDir()
	app := filepath.Join(home, "QKA.app", "Contents")
	exe := filepath.Join(app, "MacOS", "qka")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	info := `<plist><dict><key>CFBundleIdentifier</key><string>com.qka.agent</string></dict></plist>`
	if err := os.WriteFile(filepath.Join(app, "Info.plist"), []byte(info), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := string(Plist("com.qka.agent", exe)); !strings.Contains(got,
		"<key>AssociatedBundleIdentifiers</key>\n  <array><string>com.qka.agent</string></array>") {
		t.Fatalf("LaunchAgent 应关联 QKA App bundle:\n%s", got)
	}
}

func TestAutostartOnOff(t *testing.T) {
	home := t.TempDir()
	if IsEnabled(home, "") {
		t.Fatal("默认应未启用")
	}
	if err := On(home, "/Applications/qka.app/qka", ""); err != nil {
		t.Fatal(err)
	}
	if !IsEnabled(home, "") {
		t.Fatal("On 后应启用")
	}
	if err := Off(home, ""); err != nil {
		t.Fatal(err)
	}
	if IsEnabled(home, "") {
		t.Fatal("Off 后应未启用")
	}
	// 再次 Off 幂等。
	if err := Off(home, ""); err != nil {
		t.Fatalf("重复 Off 应幂等，实际 %v", err)
	}
}
