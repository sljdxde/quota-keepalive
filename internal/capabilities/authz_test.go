package capabilities

import (
	"errors"
	"testing"
)

func TestGrantAuthorizesDeclaredNeeds(t *testing.T) {
	g := GrantFromNeeds([]string{"http.outbound", "log.write"})
	for _, hook := range []string{"http.outbound", "log.write"} {
		if err := g.Authorize(hook); err != nil {
			t.Errorf("已声明钩子 %s 应被授权，实际 %v", hook, err)
		}
	}
}

func TestGrantRejectsUndeclaredHook(t *testing.T) {
	g := GrantFromNeeds([]string{"http.outbound"})
	err := g.Authorize("credentials.resolve")
	if !errors.Is(err, ErrHookNotGranted) {
		t.Fatalf("未声明钩子应被拒绝，实际 %v", err)
	}
}

func TestGrantRejectsWhenNeedsEmpty(t *testing.T) {
	g := GrantFromNeeds(nil)
	if err := g.Authorize("http.outbound"); !errors.Is(err, ErrHookNotGranted) {
		t.Fatalf("空 needs 应拒绝一切钩子，实际 %v", err)
	}
}
