//go:build darwin

package autostart

import (
	"bytes"
	"encoding/xml"
	"os"
	"path/filepath"
	"regexp"
)

// Plist renders a user LaunchAgent for the menu-bar process. Paths are XML
// escaped and the app's own bundle identifier is included when available.
func Plist(label, programPath string) []byte {
	if label == "" {
		label = DefaultLabel
	}
	escapedLabel := xmlEscape(label)
	escapedProgram := xmlEscape(programPath)
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + escapedLabel + `</string>
  <key>ProgramArguments</key>
  <array>
    <string>` + escapedProgram + `</string>
    <string>` + Arg + `</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>LimitLoadToSessionType</key>
  <string>Aqua</string>
  <key>ProcessType</key>
  <string>Interactive</string>
  <key>AbandonProcessGroup</key>
  <true/>` + associatedBundleIdentifiers(programPath) + `
</dict>
</plist>
`)
}

func xmlEscape(value string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(value))
	return b.String()
}

var bundleIDPattern = regexp.MustCompile(`<key>CFBundleIdentifier</key>\s*<string>([^<]+)</string>`)

func associatedBundleIdentifiers(programPath string) string {
	macos := filepath.Dir(programPath)
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
	return "\n  <key>AssociatedBundleIdentifiers</key>\n  <array><string>" +
		xmlEscape(string(match[1])) + "</string></array>"
}

func plistPath(homeDir, label string) string {
	if label == "" {
		label = DefaultLabel
	}
	return filepath.Join(homeDir, "Library", "LaunchAgents", label+".plist")
}

// On writes the LaunchAgent used at the next login.
func On(homeDir, programPath, label string) error {
	path := plistPath(homeDir, label)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, Plist(label, programPath), 0o644)
}

// Off removes the LaunchAgent. A missing file is already off.
func Off(homeDir, label string) error {
	err := os.Remove(plistPath(homeDir, label))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// IsEnabled reports whether the LaunchAgent record exists.
func IsEnabled(homeDir, label string) bool {
	_, err := os.Stat(plistPath(homeDir, label))
	return err == nil
}
