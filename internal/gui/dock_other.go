//go:build !darwin || ios

package gui

// Windows 任务栏图标由可执行文件资源和窗口样式管理；该设置仅作用于 macOS Dock。
func setDockIconVisible(bool) {}
