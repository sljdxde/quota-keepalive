package adapterwiring

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"qka/internal/store"
)

// TestLogWriteHookRedactsMessage 覆盖 task-3 的 adapter 写入口：供应商插件通过
// log.write 钩子提交的任何文案，落库前都必须按统一规则脱敏，且保留中文与
// 可读掩码，不能整行丢弃。
func TestLogWriteHookRedactsMessage(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	mux := NewHookMux()
	RegisterLogWrite(mux, st.DB())
	hook := mux.hooks["log.write"]
	if hook == nil {
		t.Fatal("log.write 钩子未注册")
	}

	payload := `{"target":"wb","accountId":"a1","operation":"checkin",` +
		`"level":"error","message":"登录失败 Authorization: Bearer abcdefghijklmnop，` +
		`uid:1234567890，手机 13812345678，邮箱 alice@example.com，` +
		`token=` + strings.Repeat("k", 60) + `"}`
	if _, err := hook(context.Background(), json.RawMessage(payload)); err != nil {
		t.Fatalf("log.write 钩子失败: %v", err)
	}

	var stored string
	if err := st.DB().QueryRowContext(context.Background(),
		`SELECT message FROM logs WHERE target = ? AND account_id = ?`, "wb", "a1").
		Scan(&stored); err != nil {
		t.Fatalf("未查到日志: %v", err)
	}
	for _, forbidden := range []string{
		"abcdefghijklmnop", "1234567890", "13812345678",
		"alice@example.com", strings.Repeat("k", 60),
	} {
		if strings.Contains(stored, forbidden) {
			t.Fatalf("插件日志仍包含明文 %q: %s", forbidden, stored)
		}
	}
	for _, want := range []string{
		"登录失败", "[redacted]", "uid:12***90", "138****5678", "a***@example.com",
	} {
		if !strings.Contains(stored, want) {
			t.Errorf("脱敏后的插件日志缺少 %q: %s", want, stored)
		}
	}
}
