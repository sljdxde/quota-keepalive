package gui

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// hardcodedFontSize 匹配写死的像素字号（`font-size: 13px`），
// 但放过走阶梯变量的写法（`font-size: var(--fs-md)`）。
var hardcodedFontSize = regexp.MustCompile(`font-size:\s*[0-9.]+\s*px`)

// TestPagesUseSharedFontScale 保证主窗口、菜单栏面板、设置窗口共用同一套字号阶梯。
//
// 背景：三个界面各自维护字号，出现了 10.8/11.2/11.8px 这类半像素手调值，
// 同一窗口里字号种类一多，层级就看不出差别，用户会直接说「这里的字太大了」。
// 现在只允许 --fs-* 这几档；本测试挡住任何新增的写死字号。
func TestPagesUseSharedFontScale(t *testing.T) {
	pages := []string{
		"frontend/main.html",
		"frontend/panel.html",
		"frontend/settings.html",
	}
	for _, name := range pages {
		data, err := fs.ReadFile(frontendFS, name)
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", name, err)
		}
		source := string(data)
		if matches := hardcodedFontSize.FindAllString(source, -1); len(matches) > 0 {
			t.Errorf("%s 仍有写死的字号 %v：统一改用 --fs-* 阶梯变量", name, matches)
		}
		// 阶梯本身必须定义在这一页可用的作用域里。
		for _, token := range []string{"--fs-3xs:", "--fs-2xs:", "--fs-xs:", "--fs-sm:", "--fs-md:"} {
			if !strings.Contains(source, token) {
				t.Errorf("%s 缺少字号阶梯变量 %q", name, token)
			}
		}
	}
}

// TestPagesShareIdenticalScaleValues 保证三页的阶梯数值完全一致，
// 否则「统一」只是名义上的。
func TestPagesShareIdenticalScaleValues(t *testing.T) {
	read := func(name string) string {
		data, err := fs.ReadFile(frontendFS, name)
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", name, err)
		}
		return string(data)
	}
	shared := []string{"--fs-3xs", "--fs-2xs", "--fs-xs", "--fs-sm", "--fs-md", "--fs-lg"}
	valueOf := func(source, token string) string {
		index := strings.Index(source, token+":")
		if index < 0 {
			return ""
		}
		rest := source[index+len(token)+1:]
		end := strings.IndexAny(rest, ";")
		if end < 0 {
			return ""
		}
		return strings.TrimSpace(rest[:end])
	}
	mainPage := read("frontend/main.html")
	for _, page := range []string{"frontend/panel.html", "frontend/settings.html"} {
		source := read(page)
		for _, token := range shared {
			want := valueOf(mainPage, token)
			got := valueOf(source, token)
			if want == "" || got == "" {
				t.Errorf("%s 缺少 %s 定义", page, token)
				continue
			}
			if want != got {
				t.Errorf("%s 的 %s = %q，与主窗口的 %q 不一致", page, token, got, want)
			}
		}
	}
}
