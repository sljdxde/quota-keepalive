package gui

import (
	"regexp"
	"strings"
	"testing"
)

// TestInlineScriptDeclaresBeforeUse 挡住一类只有运行时才暴露的内联脚本错误：
// 模块作用域用 const 声明的容器，如果在声明语句之前就被使用，会触发
// 「Cannot access 'X' before initialization」的 TDZ 错误，整个渲染函数抛出，
// 页面直接空白——而语法检查(node --check)与 Go 侧契约测试都发现不了。
//
// 真实事故：chipByModelId 的声明被放在使用它的 chips 循环之后，
// 结果「签到管理」进去一片空白。
func TestInlineScriptDeclaresBeforeUse(t *testing.T) {
	page := accountPage(t)
	start := strings.Index(page, `<script type="module">`)
	if start < 0 {
		t.Fatal("main.html 缺少内联模块脚本")
	}
	start += len(`<script type="module">`)
	end := strings.Index(page[start:], "</script>")
	if end < 0 {
		t.Fatal("内联模块脚本没有闭合")
	}
	script := page[start : start+end]

	// 只检查模块顶层用 const 初始化的 Map/Set：这类容器必然是「先声明后使用」，
	// 不像函数声明可以提升。
	declPattern := regexp.MustCompile("(?m)^\\s*const\\s+([A-Za-z_$][\\w$]*)\\s*=\\s*new\\s+(Map|Set)\\b")
	uses := []string{".set(", ".has(", ".get(", ".add(", ".delete(", "].", "("}

	for _, match := range declPattern.FindAllStringSubmatchIndex(script, -1) {
		name := script[match[2]:match[3]]
		declIndex := match[0]
		usePattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
		for _, loc := range usePattern.FindAllStringIndex(script, -1) {
			// 声明语句自身内部的引用（const X = new Map() 里没有 X）不算。
			if loc[0] >= declIndex && loc[0] <= match[1] {
				continue
			}
			if loc[0] < declIndex {
				line := strings.Count(script[:loc[0]], "\n") + 1
				found := false
				for _, suffix := range uses {
					rest := script[loc[1]:]
					if strings.HasPrefix(rest, suffix) {
						found = true
						break
					}
				}
				if !found {
					continue
				}
				t.Fatalf("内联脚本第 %d 行在声明之前使用了 %q（第 %d 行才声明）："+
					"这会在运行时抛 TDZ 错误并让页面空白",
					line, name, strings.Count(script[:declIndex], "\n")+1)
			}
			break
		}
	}
}
