package gui

import (
	"strings"
	"testing"
)

func TestLogsUseTopLevelCategoryTabs(t *testing.T) {
	data, err := frontendFS.ReadFile("frontend/main.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, want := range []string{
		`id="log-categories"`,
		`data-log-category="all"`,
		`data-log-category="schedule"`,
		`data-log-category="checkin"`,
		`data-log-category="refresh"`,
		`data-log-category="model_test"`,
		"logCategoryOperations",
		`schedule: "schedule"`,
		`refresh: "refresh"`,
		`model_test: "model_test"`,
		`model_test: "模型测试"`,
		"appendLogs(true)",
		"maskLogAccount(e.account_id)",
		`return "****" + text.slice(-4)`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("日志页缺少顶部分类能力 %q", want)
		}
	}
	if strings.Contains(page, `id="log-operation"`) {
		t.Error("日志类别不应继续使用底部操作输入框")
	}
	if strings.Contains(page, `data-log-category="keepalive"`) {
		t.Error("保活已全局隐藏，日志页不应保留保活分类")
	}
}
