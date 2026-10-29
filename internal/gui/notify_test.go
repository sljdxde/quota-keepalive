package gui

import (
	"context"
	"strings"
	"testing"
)

func TestMacNotifierUsesOsascriptDisplayNotification(t *testing.T) {
	var gotName string
	var gotArgs []string
	n := &MacNotifier{
		run: func(ctx context.Context, name string, args ...string) error {
			gotName = name
			gotArgs = args
			return nil
		},
	}

	if err := n.Notify(context.Background(), "qka", "签到完成"); err != nil {
		t.Fatal(err)
	}

	if gotName != "osascript" {
		t.Fatalf("应使用 osascript，实际 %s", gotName)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "display notification") ||
		!strings.Contains(joined, "签到完成") || !strings.Contains(joined, "qka") {
		t.Fatalf("应下发 display notification 且含标题与正文，实际 %v", gotArgs)
	}
}

func TestNoopNotifierDoesNothing(t *testing.T) {
	if err := (NoopNotifier{}).Notify(context.Background(), "x", "y"); err != nil {
		t.Fatalf("noop 通知器不应报错，实际 %v", err)
	}
}
