package gui

import "strings"

// BadgeClass 把操作状态映射为徽标样式：失败红色、成功绿色、待处理黄色。
func BadgeClass(state string) string {
	switch {
	case strings.Contains(state, "failed"):
		return "failed"
	case strings.Contains(state, "confirmed") || strings.Contains(state, "completed"):
		return "ok"
	default:
		return "pending"
	}
}
