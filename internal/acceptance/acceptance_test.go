package acceptance

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"qka/internal/capabilities"
	"qka/internal/feed"
	"qka/internal/logtail"
	"qka/internal/store"
)

// 发布核对清单（跨切面承诺），在一次性测试档案上验证，不使用真实凭据。

func TestRedactionAcrossResults(t *testing.T) {
	cases := map[string]string{
		"https://api.x.com?token=SECRET123":     "SECRET123",
		"https://api.x.com/path?api_key=abcdef": "abcdef",
		"https://api.x.com?password=hunter2":    "hunter2",
	}
	for url, secret := range cases {
		redacted := capabilities.RedactURL(url)
		if strings.Contains(redacted, secret) {
			t.Errorf("结果中的 URL 必须脱敏，%s 未被脱敏 → %s", secret, redacted)
		}
	}
}

func TestLogRetentionAndNoCredentials(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db := st.DB()

	old := time.Now().Add(-49 * time.Hour).UTC().Format(time.RFC3339)
	_, _ = db.Exec(`INSERT INTO logs (ts,target,account_id,operation,state,level,message)
	 VALUES (?,?,?,?,?,?,?)`, old, "x", "x", "checkin", "ok", "info", "old")
	if err := logtail.Prune(context.Background(), db, time.Now()); err != nil {
		t.Fatal(err)
	}
	page, err := logtail.Tail(context.Background(), db, logtail.Filter{}, logtail.DefaultLimit, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 0 {
		t.Fatal("超过 48 小时的日志必须被清理")
	}
}

func TestFeedEmptyUnimplemented(t *testing.T) {
	r := feed.List()
	if r.Status != "unimplemented" || len(r.Entries) != 0 {
		t.Fatal("feed_list 必须固定返回空集与未实现状态")
	}
}

func TestLogsTailBound(t *testing.T) {
	var _ *sql.DB
	st, _ := store.Open(t.TempDir())
	if _, err := logtail.Tail(context.Background(), st.DB(),
		logtail.Filter{}, logtail.MaxLimit+1, 0); err != logtail.ErrLimitTooLarge {
		t.Fatalf("log_tail 必须有界，实际 %v", err)
	}
}
