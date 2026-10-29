// Package autostart 管理开机自启（macOS LaunchAgent），提供 qka autostart on|off。
package autostart

import (
	"os"
	"path/filepath"
)

// DefaultLabel 是 LaunchAgent 的固定 label。
const DefaultLabel = "com.qka.agent"

// Plist 生成 LaunchAgent plist 内容（登录后自动运行 qka tray）。
func Plist(label, programPath string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + label + `</string>
  <key>ProgramArguments</key>
  <array>
    <string>` + programPath + `</string>
    <string>tray</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
</dict>
</plist>
`)
}

// plistPath 返回 LaunchAgents 下的 plist 路径。
func plistPath(homeDir, label string) string {
	return filepath.Join(homeDir, "Library", "LaunchAgents", label+".plist")
}

// On 写入 LaunchAgent plist（目录不存在则创建）。
func On(homeDir, programPath, label string) error {
	if label == "" {
		label = DefaultLabel
	}
	dir := filepath.Join(homeDir, "Library", "LaunchAgents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(plistPath(homeDir, label), Plist(label, programPath), 0o644)
}

// Off 删除 LaunchAgent plist（不存在视为成功）。
func Off(homeDir, label string) error {
	if label == "" {
		label = DefaultLabel
	}
	err := os.Remove(plistPath(homeDir, label))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// IsEnabled 报告自启是否已配置。
func IsEnabled(homeDir, label string) bool {
	if label == "" {
		label = DefaultLabel
	}
	_, err := os.Stat(plistPath(homeDir, label))
	return err == nil
}
