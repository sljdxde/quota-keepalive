package selfupdate

import (
	"context"
	"testing"
	"time"
)

func feedJSON(version string) []byte {
	return []byte(`{"stable":{"version":"` + version +
		`","url":"https://official.example/qka/qka-` + version + `.tar.gz","sha256":"abc"}}`)
}

func TestCheckForUpdate(t *testing.T) {
	r, err := CheckForUpdate("1.0.0", feedJSON("1.1.0"), "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Version != "1.1.0" {
		t.Fatalf("应发现 1.1.0，实际 %s", r.Version)
	}
}

func TestNoUpdate(t *testing.T) {
	if _, err := CheckForUpdate("1.1.0", feedJSON("1.1.0"), ""); err != ErrNoUpdate {
		t.Fatalf("已是最新应返回 ErrNoUpdate，实际 %v", err)
	}
}

func TestCheckForUpdateRejectsMalformedFeed(t *testing.T) {
	if _, err := CheckForUpdate("1.0.0", []byte("{"), ""); err == nil {
		t.Fatal("malformed feed must fail")
	}
}

func TestMirrorOnlyReplacesPrefix(t *testing.T) {
	r, err := CheckForUpdate("1.0.0", feedJSON("1.1.0"), "https://mirror.cn")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://mirror.cn/qka/qka-1.1.0.tar.gz"
	if r.URL != want {
		t.Errorf("镜像应仅替换下载前缀，实际 %s，期望 %s", r.URL, want)
	}
	if r.SHA256 != "abc" {
		t.Error("镜像不得改变哈希")
	}
}

func TestMirrorWithPathRejected(t *testing.T) {
	if _, err := CheckForUpdate("1.0.0", feedJSON("1.1.0"),
		"https://evil.cn/repo"); err != ErrBadMirror {
		t.Fatalf("镜像携带路径应拒绝，实际 %v", err)
	}
}

func TestDryRunPlan(t *testing.T) {
	r, _ := CheckForUpdate("1.0.0", feedJSON("1.1.0"), "")
	plan := DryRun("1.0.0", *r)
	if plan.OldPath != "qka.old" || plan.Undo == "" || plan.Verify == "" {
		t.Fatal("dry-run 应包含旧版保留、健康检查与回滚说明")
	}
}

func TestWaitIdle(t *testing.T) {
	// 先忙后闲。
	var calls int
	idle := func() bool {
		calls++
		return calls >= 3
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := WaitIdle(ctx, idle, 10*time.Millisecond); err != nil {
		t.Fatalf("应变忙为闲，实际 %v", err)
	}
}

func TestWaitIdleTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := WaitIdle(ctx, func() bool { return false }, 10*time.Millisecond); err == nil {
		t.Fatal("持续忙且超时应返回错误")
	}
}
