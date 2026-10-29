package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func newTestEnv(args ...string) (*bytes.Buffer, *bytes.Buffer, *Env) {
	var out, errBuf bytes.Buffer
	env := &Env{
		Args:   args,
		Stdout: &out,
		Stderr: &errBuf,
		Getenv: func(string) string { return "" },
	}
	return &out, &errBuf, env
}

func decodeJSON(t *testing.T, b *bytes.Buffer) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(b.Bytes()), &got); err != nil {
		t.Fatalf("输出不是合法 JSON: %v\n%s", err, b.String())
	}
	return got
}

func TestNoArgsPrintsUsageListingAllModes(t *testing.T) {
	out, _, env := newTestEnv()
	code := Run(context.Background(), env)
	if code != 0 {
		t.Fatalf("期望退出码 0，实际 %d", code)
	}
	help := out.String()
	for _, mode := range []string{"serve", "tray", "panel", "bridge", "tui", "doctor", "update", "autostart"} {
		if !strings.Contains(help, mode) {
			t.Errorf("帮助文本缺少形态 %q\n%s", mode, help)
		}
	}
}

func TestUnknownCommandReturnsErrorCode(t *testing.T) {
	_, _, env := newTestEnv("nope", "--json")
	code := Run(context.Background(), env)
	if code != 2 {
		t.Fatalf("未知命令期望退出码 2，实际 %d", code)
	}
}

func TestUnimplementedModeReportsStableJSONStatus(t *testing.T) {
	// tray 已落地为真实菜单栏形态；其余形态保持显式未实现。
	for _, mode := range []string{"panel", "bridge", "tui", "update"} {
		out, _, env := newTestEnv(mode, "--json")
		code := Run(context.Background(), env)
		if code != 0 {
			t.Fatalf("形态 %s 期望退出码 0（显式未实现状态），实际 %d", mode, code)
		}
		got := decodeJSON(t, out)
		if got["ok"] != true {
			t.Errorf("形态 %s 期望 ok=true，实际 %v", mode, got["ok"])
		}
		data, _ := got["data"].(map[string]any)
		if data == nil || data["status"] != "unimplemented" {
			t.Errorf("形态 %s 期望 data.status=unimplemented，实际 %v", mode, got["data"])
		}
	}
}

func TestErrorsAreJSONWhenJSONFlagSet(t *testing.T) {
	_, errBuf, env := newTestEnv("nope", "--json")
	Run(context.Background(), env)
	got := decodeJSON(t, errBuf)
	if got["ok"] != false {
		t.Errorf("错误 JSON 期望 ok=false，实际 %v", got["ok"])
	}
	if _, has := got["error"]; !has {
		t.Errorf("错误 JSON 缺少 error 字段: %v", got)
	}
}
