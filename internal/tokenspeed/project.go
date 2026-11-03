package tokenspeed

import (
	"path/filepath"
	"strconv"
	"strings"
)

// projectName 从会话文件路径推导「给人看的」项目/工作区名。
//
// 只读路径与目录名，绝不读取提示词或回复内容：Token 速度面板是纯本地统计，
// 展示出去的名字也不应该泄露对话内容。
//
// 不同 Agent 的目录布局：
//
//	DSH     ~/.dsh/sessions/<workspace按路径编码>/session-<uuid>/session.v4.jsonl.zstd
//	Codex   ~/.codex/sessions/<年>/<月>/<日>/rollout-*.jsonl      （项目名来自 session_meta.cwd）
//	Qoder   ~/.qoder/projects/<按路径编码的项目目录>/*.jsonl
//	Claude  ~/.claude/projects/<按路径编码的项目目录>/*.jsonl
func projectName(source, path string) string {
	switch source {
	case "dsh":
		// 工作区目录是 session-<uuid> 的父目录。
		sessionDir := filepath.Dir(path)
		workspace := filepath.Base(filepath.Dir(sessionDir))
		return decodeWorkspaceDir(workspace)
	case "qoder", "qoder-cn", "claude":
		return decodeWorkspaceDir(filepath.Base(filepath.Dir(path)))
	default:
		return ""
	}
}

// decodeWorkspaceDir 还原 DSH/Qoder 一类的编码目录名：
//
//	--Users-yuzhou-004~4E2A~4EBA-quota-keepalive-agent--
//	→ quota-keepalive-agent
//
// 规则（按实测目录名反推）：首尾各一个 "--" 包住绝对路径；路径分隔符写成 "-"；
// 非 ASCII 码点写成 "~" + 大写十六进制（4 位及以上）。
// 目录名不是这种编码时原样返回，避免把普通名字改坏。
func decodeWorkspaceDir(dir string) string {
	if dir == "" || dir == "." || dir == string(filepath.Separator) {
		return ""
	}
	trimmed := dir
	wrapped := strings.HasPrefix(trimmed, "--") && strings.HasSuffix(trimmed, "--")
	if wrapped {
		trimmed = strings.TrimSuffix(strings.TrimPrefix(trimmed, "--"), "--")
	} else if strings.HasSuffix(dir, "--") {
		// 未加包裹却以连续 '-' 结尾：说明原路径最后一段文字在编码时被换成了
		// 分隔符（非 ASCII 项目名丢失），这种情况宁可不给名字，让界面回落到
		// 模型名，也不要显示半截无意义的用户路径片段。
		return ""
	}
	if trimmed == "" {
		return ""
	}
	var builder strings.Builder
	for i := 0; i < len(trimmed); i++ {
		ch := trimmed[i]
		switch {
		case ch == '~':
			// 连续 4 位以上的十六进制码点。
			j := i + 1
			for j < len(trimmed) && isHexDigit(trimmed[j]) && j-i <= 6 {
				j++
			}
			if code, err := strconv.ParseInt(trimmed[i+1:j], 16, 32); err == nil && code > 0 {
				builder.WriteRune(rune(code))
				i = j - 1
				continue
			}
			builder.WriteByte(ch)
		case ch == '-':
			builder.WriteString("/")
		default:
			builder.WriteByte(ch)
		}
	}
	decoded := strings.Trim(builder.String(), "/")
	if decoded == "" {
		return ""
	}
	// 取最后一段作为项目名；整段都是分隔符时回退为空（界面会用模型名兜底）。
	name := filepath.Base(decoded)
	if strings.Trim(name, "/.-") == "" {
		return ""
	}
	return name
}

func isHexDigit(ch byte) bool {
	return (ch >= '0' && ch <= '9') || (ch >= 'A' && ch <= 'F') || (ch >= 'a' && ch <= 'f')
}

// projectBaseName 取绝对路径的最后一段作为项目名；根目录等无意义路径返回空。
func projectBaseName(path string) string {
	cleaned := strings.TrimSpace(path)
	if cleaned == "" {
		return ""
	}
	name := filepath.Base(filepath.Clean(cleaned))
	if name == "" || name == "." || name == string(filepath.Separator) ||
		strings.Trim(name, "/.-") == "" {
		return ""
	}
	return name
}

// sessionDisplayLabel 决定会话行显示什么名字：
// 项目/工作区 > 模型名 > 短 ID。三者都可能为空，最后一定回落到「会话 <id>」。
func sessionDisplayLabel(project, model, agentID string) string {
	if name := strings.TrimSpace(project); name != "" {
		return name
	}
	if name := strings.TrimSpace(model); name != "" {
		return name
	}
	return "会话 " + sessionID(agentID)
}
