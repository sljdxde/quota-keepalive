package feedback

import "qka/internal/privacy"

// SanitizeText 复用 internal/privacy 的统一脱敏规则：去掉控制字节、脱敏
// 凭证与身份标识，并按 maxRunes 截断（<=0 不截断）。反馈通道与日志通道
// 必须共用同一套规则，避免两处口径漂移。
func SanitizeText(value string, maxRunes int) string {
	return privacy.SanitizeText(value, maxRunes)
}
