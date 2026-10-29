package gui

// PluginRow 是插件页一行适配器。
type PluginRow struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Version      string `json:"version"`
	Source       string `json:"source"`        // market / local
	Signed       bool   `json:"signed"`        // 签名状态
	Verified     bool   `json:"verified"`      // 未签名则 false（标注未验证）
	UpdateStatus string `json:"update_status"` // up_to_date / update_available / unverified
}

// AuditLine 是一次受限加载的审计记录（插件页可见）。
type AuditLine struct {
	PackageName string `json:"package_name"`
	Action      string `json:"action"`
	TS          string `json:"ts"`
}

// PluginsView 是插件页视图。
type PluginsView struct {
	Plugins []PluginRow `json:"plugins"`
	// DevMode 是开发者模式开关状态。
	DevMode bool `json:"dev_mode"`
	// Audit 是最近的受限加载审计。
	Audit []AuditLine `json:"audit"`
}

// NewPluginsView 组装插件页视图；未签名适配器固定标注未验证。
func NewPluginsView(rows []PluginRow, devMode bool, audit []AuditLine) PluginsView {
	for i := range rows {
		if !rows[i].Signed {
			rows[i].Verified = false
			rows[i].UpdateStatus = "unverified"
		}
	}
	return PluginsView{Plugins: rows, DevMode: devMode, Audit: audit}
}
