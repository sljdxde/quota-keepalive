package runlock

import "testing"

func TestAcquireNamedRejectsInvalidNames(t *testing.T) {
	for _, name := range []string{"", ".", "..", "a/b", `a\b`} {
		t.Run(name, func(t *testing.T) {
			if lock, err := AcquireNamed(t.TempDir(), name); lock != nil || err == nil {
				t.Fatalf("invalid name %q should fail: lock=%v err=%v", name, lock, err)
			}
		})
	}
}
