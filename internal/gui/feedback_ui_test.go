package gui

import (
	"strings"
	"testing"
)

func TestFeedbackDialogSupportsSafeAnonymousIssueReports(t *testing.T) {
	page := accountPage(t)
	for _, want := range []string{
		// 入口先隐藏（服务端未部署），但界面与提交能力必须保留，改回一行开关即可恢复。
		`const FEEDBACK_AVAILABLE = false`,
		`if (!FEEDBACK_AVAILABLE) return`,
		`node.hidden = !FEEDBACK_AVAILABLE`,
		`aria-label="反馈问题" hidden`,
		`id="logs-feedback" type="button" hidden`,
		`id="feedback-dialog"`,
		`id="feedback-description"`,
		`id="feedback-files"`,
		`accept="image/png,image/jpeg,image/webp"`,
		`id="feedback-include-logs"`,
		`Bound.FeedbackContext()`,
		`Bound.FeedbackSubmit(`,
		`feedbackDialog.addEventListener("paste"`,
		`event.clipboardData`,
		`addFeedbackFiles(event.dataTransfer && event.dataTransfer.files)`,
		`canvas.toDataURL("image/jpeg", 0.82)`,
		`const maxSide = 1600`,
		`最多上传 6 张图片`,
		`文本和异常日志会在上传前自动脱敏`,
		`图片中的可见敏感信息无法可靠自动识别，请先遮盖`,
		`navigator.clipboard.writeText(result.url || "")`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("反馈界面缺少安全提交能力 %q", want)
		}
	}
	for _, forbidden := range []string{
		"raw.githubusercontent.com",
		"GITHUB_TOKEN",
		"Authorization: Bearer",
	} {
		if strings.Contains(page, forbidden) {
			t.Errorf("反馈前端不应持有或暴露 GitHub 写权限 %q", forbidden)
		}
	}
}

func TestFeedbackBindingsExist(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/bindings/qka/internal/gui/boundservice.js")
	if err != nil {
		t.Fatal(err)
	}
	bindings := string(data)
	for _, want := range []string{
		"export function FeedbackContext()",
		"export function FeedbackSubmit(description, images, includeLogs)",
		"$Call.ByID(654170201)",
		"$Call.ByID(1874302078",
	} {
		if !strings.Contains(bindings, want) {
			t.Errorf("反馈绑定缺少 %q", want)
		}
	}
}
