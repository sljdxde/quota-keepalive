package features

import "testing"

func TestKeepaliveIsGloballyHidden(t *testing.T) {
	if KeepaliveAvailable {
		t.Fatal("keepalive must remain hidden until explicitly re-enabled")
	}
	if OperationAvailable("keepalive") {
		t.Fatal("keepalive operation must be unavailable")
	}
	if !OperationAvailable("checkin") {
		t.Fatal("checkin operation must remain available")
	}
	for _, action := range []string{"", "all", "keepalive"} {
		if got := NormalizeAction(action); got != "checkin" {
			t.Fatalf("NormalizeAction(%q) = %q, want checkin", action, got)
		}
	}
	if got := NormalizeAction("unknown"); got != "unknown" {
		t.Fatalf("unknown action must be preserved, got %q", got)
	}
	for _, action := range []string{"", "all", "checkin", "keepalive"} {
		if got := normalizeAction(action, true); got != action {
			t.Fatalf("enabled normalizeAction(%q) = %q", action, got)
		}
	}
}
