package scheduler

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var bundleIDPattern = regexp.MustCompile(`<key>CFBundleIdentifier</key>\s*<string>([^<]+)</string>`)

// LaunchdPlist renders a user LaunchAgent that runs QKA task-run on a fixed
// interval. StartInterval is relative to the agent load time.
// All user-controlled strings are XML escaped.
func LaunchdPlist(spec Spec) ([]byte, error) {
	if !executableOK(spec.Executable) {
		return nil, fmt.Errorf("scheduler: invalid executable")
	}
	if spec.IntervalMinutes < 5 || spec.IntervalMinutes > 7*24*60 {
		return nil, fmt.Errorf("scheduler: invalid interval")
	}
	label := LaunchdLabel(spec.ConfigDir)
	esc := func(value string) string {
		var b bytes.Buffer
		_ = xml.EscapeText(&b, []byte(value))
		return b.String()
	}
	intervalSeconds := spec.IntervalMinutes * 60
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>task-run</string>
    <string>--scheduled</string>
    <string>--config-dir</string>
    <string>%s</string>
  </array>
  <key>StartInterval</key>
  <integer>%d</integer>
  <key>RunAtLoad</key>
  <true/>
  <key>ProcessType</key>
  <string>Background</string>
  <key>AbandonProcessGroup</key>
  <true/>%s
</dict>
</plist>
`, esc(label), esc(spec.Executable), esc(spec.ConfigDir), intervalSeconds,
		associatedBundleIdentifiers(spec.Executable))), nil
}

// associatedBundleIdentifiers makes System Settings list the LaunchAgent under
// QKA's bundle name rather than the signing identity. A bare executable has no
// associated app and therefore emits no key.
func associatedBundleIdentifiers(executable string) string {
	macos := filepath.Dir(executable)
	if filepath.Base(macos) != "MacOS" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(macos), "Info.plist"))
	if err != nil {
		return ""
	}
	match := bundleIDPattern.FindSubmatch(data)
	if len(match) != 2 {
		return ""
	}
	var escaped bytes.Buffer
	_ = xml.EscapeText(&escaped, match[1])
	return "\n  <key>AssociatedBundleIdentifiers</key>\n  <array><string>" +
		escaped.String() + "</string></array>"
}
