// Package autostart manages QKA's login item using the current user's native
// mechanism: LaunchAgent on macOS and the HKCU Run key on Windows.
package autostart

// DefaultLabel is the fixed identifier used by the macOS LaunchAgent. Windows
// always writes the single QKA value under the current user's Run key.
const DefaultLabel = "com.qka.agent"

// Arg is the program argument used at login: stay in the menu bar.
const Arg = "tray"
