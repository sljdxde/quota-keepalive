package privacy

import (
	"strings"
	"testing"
)

// TestSanitizeTextRedactsLogLeaks 覆盖日志/反馈最常见的泄露样例：
// 写回读到的文本必须只保留可读掩码，不得残留明文。
func TestSanitizeTextRedactsLogLeaks(t *testing.T) {
	input := strings.Join([]string{
		"Authorization: Bearer abcdefghijklmnop",
		"api_key=sk-1234567890abcdef",
		`{"refresh_token":"rt-secret-value","uid":"1234567890"}`,
		"email alice@example.com phone 13812345678",
		"uid:1234567890 userId=alice-2024 account_id:wx_abcdef123456",
		"path /Users/yuzhou/project",
		"jwt " + strings.Repeat("a", 60),
	}, "\n")
	got := SanitizeText(input, 0)

	for _, forbidden := range []string{
		"abcdefghijklmnop", "sk-1234567890abcdef", "rt-secret-value",
		"alice@example.com", "13812345678", "yuzhou", "1234567890",
		"alice-2024", "wx_abcdef123456", strings.Repeat("a", 60),
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("sanitized text still contains %q:\n%s", forbidden, got)
		}
	}
	for _, want := range []string{
		"[redacted]", "a***@example.com", "138****5678", "/Users/<user>",
		"uid:12***90", "userId=al***24", "account_id:wx***56",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("sanitized text missing %q:\n%s", want, got)
		}
	}
}

// TestSanitizeTextKeepsChineseAndNumbers 验证正常文案不被误伤：中文、普通数字、
// 时间、金额和短标识都必须原样保留。
func TestSanitizeTextKeepsChineseAndNumbers(t *testing.T) {
	cases := []string{
		"签到成功，今日已领取 +100 积分，当前额度 2351.71",
		"自动执行：尝试 1，成功 1，需处理 0，跳过 2",
		"模型测试 · hy3 · HTTP 200 · 837 ms · 模型可用",
		"冷却至 15:04，明天再试",
		"账号 ****7198 登录成功",
	}
	for _, value := range cases {
		if got := SanitizeText(value, 0); got != value {
			t.Errorf("正常文案被误伤：\n in=%q\nout=%q", value, got)
		}
	}
}

// TestRedactMasksRemainReadable 验证脱敏结果是可读掩码而不是整行丢弃。
func TestRedactMasksRemainReadable(t *testing.T) {
	got := Redact("用户 a***@example.com 的 uid:1234567890 手机 13812345678 已登录")
	for _, want := range []string{"用户", "a***@example.com", "uid:12***90", "138****5678", "已登录"} {
		if !strings.Contains(got, want) {
			t.Errorf("masked text missing %q: %s", want, got)
		}
	}
}

// TestSanitizeTextTruncationAndControlBytes 验证控制字符清理与截断顺序。
func TestSanitizeTextTruncationAndControlBytes(t *testing.T) {
	got := SanitizeText("a\x00b\r\nc\r"+strings.Repeat("x", 10), 5)
	if strings.ContainsAny(got, "\x00") || !strings.HasSuffix(got, "…") {
		t.Fatalf("sanitized text = %q", got)
	}
	if strings.Contains(got, "\r") {
		t.Fatalf("carriage return leaked: %q", got)
	}
	if strings.HasSuffix(SanitizeText("plain", 0), "…") {
		t.Fatal("maxRunes<=0 不应截断")
	}
}

// TestSanitizeTextPlatformPathsAndShortMasks 覆盖 Linux/Windows 用户目录与
// 短 uid 的掩码分支。
func TestSanitizeTextPlatformPathsAndShortMasks(t *testing.T) {
	if got := SanitizeText("/home/alice/secret", 0); strings.Contains(got, "alice") {
		t.Fatalf("Linux 路径用户名未脱敏: %s", got)
	}
	if got := SanitizeText(`C:\Users\alice\secret`, 0); strings.Contains(got, "alice") {
		t.Fatalf("Windows 路径用户名未脱敏: %s", got)
	}
	for uid, want := range map[string]string{
		"uid:123":    "uid:1***3",
		"uid:12345":  "uid:1***5",
		"uid:123456": "uid:12***56",
	} {
		if got := Redact(uid); got != want {
			t.Errorf("Redact(%q) = %q, want %q", uid, got, want)
		}
	}
}
