package settings

import (
	"errors"
	"strings"
	"testing"
)

// TestSetStringTextField 覆盖自由文本字段（反馈服务地址）的设置、长度上限与读取。
func TestSetStringTextField(t *testing.T) {
	s := Default()
	const key = "feedback_endpoint"
	if got := s.GetString(key); got != "" {
		t.Fatalf("默认反馈地址应为空，实际 %q", got)
	}
	if err := s.SetString(key, "https://proxy.example/feedback"); err != nil {
		t.Fatalf("合法文本设置失败: %v", err)
	}
	if got := s.GetString(key); got != "https://proxy.example/feedback" {
		t.Fatalf("文本设置未生效: %q", got)
	}
	if err := s.SetString(key, strings.Repeat("x", maxTextValueBytes+1)); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("超长文本应被拒绝，实际 %v", err)
	}
	if err := s.SetString("show_dock_icon", "not-a-bool"); !errors.Is(err, ErrWrongType) {
		t.Fatalf("非字符串字段应报类型错误，实际 %v", err)
	}
}

// TestValidateTextField 覆盖 schema 校验里的文本类型/长度分支。
func TestValidateTextField(t *testing.T) {
	wrongType := &Settings{values: map[string]any{"feedback_endpoint": 42}}
	if err := wrongType.Save(t.TempDir()); !errors.Is(err, ErrWrongType) {
		t.Fatalf("文本字段存非字符串应报类型错误，实际 %v", err)
	}
	tooLong := &Settings{values: map[string]any{
		"feedback_endpoint": strings.Repeat("x", maxTextValueBytes+1),
	}}
	if err := tooLong.Save(t.TempDir()); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("超长文本应报非法值，实际 %v", err)
	}
}
