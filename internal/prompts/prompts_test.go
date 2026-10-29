package prompts

import (
	"strings"
	"testing"
)

func TestListFivePrompts(t *testing.T) {
	got := List()
	if len(got) != 5 {
		t.Fatalf("应恰好有 5 个 prompt，实际 %d", len(got))
	}
	want := map[string]bool{
		"install_and_pair": false, "add_account_with_official_login": false,
		"setup_host_schedule": false, "diagnose_failed_account": false,
		"explain_today_results": false,
	}
	for _, p := range got {
		if _, ok := want[p.Name]; ok {
			want[p.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("缺少 prompt: %s", name)
		}
	}
}

func TestSetupHostScheduleDoesNotWriteState(t *testing.T) {
	msgs, err := Render("setup_host_schedule", nil)
	if err != nil {
		t.Fatal(err)
	}
	content := msgs[0].Content
	if !strings.Contains(content, "宿主侧") {
		t.Error("应只输出宿主侧步骤")
	}
	if strings.Contains(content, "已创建") || strings.Contains(content, "已写入") {
		t.Error("setup_host_schedule 不得声称写入 QKA 状态")
	}
}

func TestRenderUnknown(t *testing.T) {
	if _, err := Render("nope", nil); err != ErrUnknownPrompt {
		t.Fatalf("未知 prompt 应报错，实际 %v", err)
	}
}

func TestAddAccountNoPasswordCollection(t *testing.T) {
	msgs, _ := Render("add_account_with_official_login", map[string]string{"target": "qoder-cn"})
	if !strings.Contains(msgs[0].Content, "不收集密码") {
		t.Error("官方登录引导应明确不收集密码/验证码")
	}
}

func TestExplainTodayResultsIncludesOwnAppFallback(t *testing.T) {
	msgs, err := Render("explain_today_results", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msgs[0].Content, "manual_required") ||
		!strings.Contains(msgs[0].Content, "请回 WorkBuddy App 签到") {
		t.Errorf("今日结果说明必须区分应用内签到兜底，实际 %s", msgs[0].Content)
	}
}
