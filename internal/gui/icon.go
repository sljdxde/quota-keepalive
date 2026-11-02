package gui

import "qka/internal/brandicon"

// templateIcon 返回单色 Q 模板图标，系统在深浅色菜单栏自动按 alpha 着色。
func templateIcon() []byte {
	return brandicon.Template()
}

// applicationIcon 返回深青底、象牙白 Q 环与暖金尾笔的应用图标，供 Wails
// 写入 Dock/任务栏窗口图标；.app 与 Windows PE 的安装图标仍由发布脚本生成。
func applicationIcon(size int) []byte {
	return brandicon.Application(size)
}
