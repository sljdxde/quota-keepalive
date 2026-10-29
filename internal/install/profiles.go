package install

// Profile 是一个宿主的经实测配置档案。
type Profile struct {
	// HostID 是宿主标识。
	HostID string
	// Label 是展示名。
	Label string
	// MCPSupported 表示该宿主是否支持 MCP 接入。
	MCPSupported bool
	// ConfigLocation 是宿主配置文件位置。
	ConfigLocation string
	// Transport 是接入传输（stdio / http）。
	Transport string
	// CredentialWrite 说明配对凭据写入位置。
	CredentialWrite string
	// ScheduleGuide 是宿主原生计划创建指引（仅观测核验）。
	ScheduleGuide string
	// Uninstall 是撤销方式。
	Uninstall string
}

// Profiles 返回五个宿主的配置档案。
func Profiles() []Profile {
	return []Profile{
		{
			HostID: "workbuddy", Label: "WorkBuddy", MCPSupported: true,
			ConfigLocation:  "~/.workbuddy/mcp.json",
			Transport:       "stdio",
			CredentialWrite: "在 mcp.json 的 `qka` 条目中写入配对凭据（仅请求头使用）",
			ScheduleGuide:   "在 WorkBuddy 自带的定时/自动化设置中创建每日任务",
			Uninstall:       "删除 mcp.json 中的 `qka` 条目",
		},
		{
			HostID: "codex", Label: "Codex", MCPSupported: true,
			ConfigLocation:  "~/.codex/config.toml",
			Transport:       "stdio",
			CredentialWrite: "在 config.toml 的 [mcp_servers.qka] 写入",
			ScheduleGuide:   "Codex 无内置调度：用系统 launchd/cron 调用，仅观测核验",
			Uninstall:       "删除 [mcp_servers.qka] 段",
		},
		{
			HostID: "zcode", Label: "ZCode", MCPSupported: true,
			ConfigLocation:  "~/.zcode/settings.json",
			Transport:       "stdio",
			CredentialWrite: "在 settings.json 的 mcpServers.qka 写入",
			ScheduleGuide:   "在 ZCode 计划设置中创建每日任务",
			Uninstall:       "删除 mcpServers.qka 键",
		},
		{
			HostID: "claude-code", Label: "Claude Code", MCPSupported: true,
			ConfigLocation:  "~/.claude.json",
			Transport:       "stdio",
			CredentialWrite: "在 claude mcp add 或 mcpServers 中写入",
			ScheduleGuide:   "Claude Code 无内置持久计划：用系统调度，仅观测核验",
			Uninstall:       "claude mcp remove qka",
		},
		{
			HostID: "opencode", Label: "OpenCode", MCPSupported: true,
			ConfigLocation:  "~/.config/opencode/opencode.json",
			Transport:       "stdio",
			CredentialWrite: "在 opencode.json 的 mcp.qka 写入",
			ScheduleGuide:   "OpenCode 无内置持久计划：用系统调度，仅观测核验",
			Uninstall:       "删除 mcp.qka 键",
		},
	}
}

// ProfileFor 按宿主 id 查找档案；未知宿主返回 false。
func ProfileFor(hostID string) (Profile, bool) {
	for _, p := range Profiles() {
		if p.HostID == hostID {
			return p, true
		}
	}
	return Profile{}, false
}
