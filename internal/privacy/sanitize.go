package privacy

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// 统一脱敏规则：日志、反馈、崩溃信息等任何离开本机或进入长期存储的文本都在
// 这里处理。规则只替换敏感片段，保留可读掩码与正常中文文案，绝不整行丢弃。

var (
	// bearerPattern 匹配 Authorization: Bearer 后跟的长凭证。
	bearerPattern = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/\-=]+`)
	// secretPattern 匹配 key=value 形式的凭证，兼容 JSON 引号包裹的键与值。
	// 分组：1=前引号 2=键 3=引号/分隔符/值前引号 4=值 5=值后引号。
	secretPattern = regexp.MustCompile(`(?i)(["']?)\b(access[_-]?token|refresh[_-]?token|id[_-]?token|api[_-]?key|authorization|cookie|password|passwd|secret|token|xsec[_-]?token|session[_-]?token)\b(["']?\s*[:=]\s*["']?)([A-Za-z0-9._~+/=:@%\-]+)(["']?)`)
	// querySecretPattern 匹配 URL 查询串里的凭证。
	querySecretPattern = regexp.MustCompile(`(?i)([?&](?:access_token|refresh_token|id_token|api_key|key|token|secret|password|xsec_token|xsec_source)=)[A-Za-z0-9%._~+/:@\-]+`)
	// identityPattern 匹配 uid/账号等稳定身份标识，保留首尾字符以便排障。
	identityPattern = regexp.MustCompile(`(?i)\b(uid|user[_-]?id|account[_-]?id|openid|unionid|device[_-]?id|x-user-id)(\s*["']?\s*[:=]\s*["']?)([A-Za-z0-9_.@\-]{3,})`)
	// vendorTokenPattern 匹配常见供应商 token 前缀；长度不足的短 token 由后续长串规则兜底。
	vendorTokenPattern = regexp.MustCompile(`(?i)\b(?:sk-[A-Za-z0-9_\-]{6,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[abposr]-[A-Za-z0-9\-]{10,}|AKIA[0-9A-Z]{16})\b`)
	// emailPattern 匹配邮箱，保留首字符与域名。
	emailPattern = regexp.MustCompile(`(?i)\b([A-Z0-9._%+\-])[A-Z0-9._%+\-]*@([A-Z0-9.\-]+\.[A-Z]{2,})\b`)
	// phonePattern 匹配中国大陆手机号，保留前 3 后 4 位。
	phonePattern = regexp.MustCompile(`\b(1[3-9]\d)\d{4}(\d{4})\b`)
	// homePattern 匹配本机用户目录，隐藏用户名。
	homePattern = regexp.MustCompile(`(?i)(?:/Users/|/home/|C:\\Users\\)[^/\s\\]+`)
	// longToken 匹配长串无分隔符 token（JWT、哈希、密钥等）。
	longToken = regexp.MustCompile(`\b[A-Za-z0-9_\-]{48,}\b`)
)

// SanitizeText 去掉控制字节、统一换行、脱敏敏感片段，并按 maxRunes 截断。
// maxRunes<=0 表示不截断。正常中文文案与普通数字不会被误伤。
func SanitizeText(value string, maxRunes int) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f || r == 0xfffd {
			return -1
		}
		return r
	}, value)
	value = Redact(value)
	value = strings.TrimSpace(value)
	if maxRunes > 0 && utf8.RuneCountInString(value) > maxRunes {
		runes := []rune(value)
		value = string(runes[:maxRunes]) + "…"
	}
	return value
}

// Redact 只做脱敏、不做控制字符清理与截断，供需要保留原排版的调用方复用。
func Redact(value string) string {
	value = bearerPattern.ReplaceAllString(value, "$1[redacted]")
	value = secretPattern.ReplaceAllString(value, "${1}${2}${3}[redacted]${5}")
	value = querySecretPattern.ReplaceAllString(value, "$1[redacted]")
	value = identityPattern.ReplaceAllStringFunc(value, maskIdentity)
	value = vendorTokenPattern.ReplaceAllString(value, "[redacted]")
	value = emailPattern.ReplaceAllString(value, "$1***@$2")
	value = phonePattern.ReplaceAllString(value, "$1****$2")
	// 避免把本机 macOS/Linux 用户目录里的用户名带进共享文本。
	value = homePattern.ReplaceAllStringFunc(value, func(match string) string {
		switch {
		case strings.HasPrefix(match, "/Users/"):
			return "/Users/<user>"
		case strings.HasPrefix(match, "/home/"):
			return "/home/<user>"
		default:
			return `C:\Users\<user>`
		}
	})
	value = longToken.ReplaceAllString(value, "[redacted]")
	return value
}

// maskIdentity 把 uid 一类标识替换成首尾可见的掩码（如 uid:12***89）。
// identityPattern 保证命中时一定有 4 个子匹配、值至少 3 个字符，因此不再保留
// 永远走不到的长度防御分支。
func maskIdentity(match string) string {
	parts := identityPattern.FindStringSubmatch(match)
	return parts[1] + parts[2] + maskValue(parts[3])
}

func maskValue(value string) string {
	runes := []rune(value)
	if len(runes) >= 6 {
		return string(runes[:2]) + "***" + string(runes[len(runes)-2:])
	}
	// 正则要求值至少 3 个字符，这里只需处理 3-5 字符的短标识。
	return string(runes[:1]) + "***" + string(runes[len(runes)-1:])
}
