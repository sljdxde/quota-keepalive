package gui

import (
	"strings"
	"testing"
)

func TestWindowsUseWailsDragRegions(t *testing.T) {
	for _, name := range []string{"frontend/main.html", "frontend/settings.html"} {
		data, err := frontendFS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		page := string(data)
		if !strings.Contains(page, "--wails-draggable: drag") {
			t.Errorf("%s 缺少 Wails 拖拽区域", name)
		}
		if strings.Contains(page, "-webkit-app-region") {
			t.Errorf("%s 仍使用 Wails v3 不识别的旧拖拽属性", name)
		}
	}
}
