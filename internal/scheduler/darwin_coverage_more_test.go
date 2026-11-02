//go:build darwin

package scheduler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDarwinInstallOperationalErrorBranches(t *testing.T) {
	ctx := context.Background()
	spec := Spec{Executable: "/tmp/qka", ConfigDir: "/tmp/q", IntervalMinutes: 30}

	t.Run("loaded check error", func(t *testing.T) {
		s := newDarwinScheduler(t.TempDir(), "501", func(
			context.Context, string, ...string,
		) ([]byte, error) {
			return []byte("permission denied"), errors.New("print failed")
		})
		if err := s.Install(ctx, spec); err == nil {
			t.Fatal("loaded check error must surface")
		}
	})

	t.Run("bootout check error", func(t *testing.T) {
		printCalls := 0
		s := newDarwinScheduler(t.TempDir(), "501", func(
			_ context.Context, _ string, args ...string,
		) ([]byte, error) {
			switch args[0] {
			case "print":
				printCalls++
				if printCalls == 1 {
					return []byte("loaded"), nil
				}
				return []byte("permission denied"), errors.New("print failed")
			case "bootout":
				return nil, errors.New("bootout failed")
			default:
				t.Fatalf("unexpected command %q", args[0])
			}
			return nil, nil
		})
		if err := s.Install(ctx, spec); err == nil {
			t.Fatal("bootout recheck error must surface")
		}
	})

	t.Run("bootout remains loaded", func(t *testing.T) {
		printCalls := 0
		s := newDarwinScheduler(t.TempDir(), "501", func(
			_ context.Context, _ string, args ...string,
		) ([]byte, error) {
			switch args[0] {
			case "print":
				printCalls++
				return []byte("loaded"), nil
			case "bootout":
				return nil, errors.New("bootout failed")
			default:
				t.Fatalf("unexpected command %q", args[0])
			}
			return nil, nil
		})
		if err := s.Install(ctx, spec); err == nil {
			t.Fatal("loaded service after bootout failure must surface")
		}
	})

	t.Run("write failure reboots previous plist", func(t *testing.T) {
		home := t.TempDir()
		oldWrite := writePlistFile
		writePlistFile = func(string, []byte, os.FileMode) error {
			return errors.New("write failed")
		}
		t.Cleanup(func() { writePlistFile = oldWrite })
		s := newDarwinScheduler(home, "501", func(
			_ context.Context, _ string, args ...string,
		) ([]byte, error) {
			switch args[0] {
			case "print":
				return []byte("loaded"), nil
			case "bootout":
				return nil, nil
			case "bootstrap":
				return nil, nil
			default:
				t.Fatalf("unexpected command %q", args[0])
			}
			return nil, nil
		})
		path := s.plistPath(spec)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := s.Install(ctx, spec); err == nil {
			t.Fatal("plist write failure must surface")
		}
	})

	t.Run("bootstrap failure joins recheck error", func(t *testing.T) {
		printCalls := 0
		s := newDarwinScheduler(t.TempDir(), "501", func(
			_ context.Context, _ string, args ...string,
		) ([]byte, error) {
			switch args[0] {
			case "print":
				printCalls++
				if printCalls == 1 {
					return []byte("Could not find service"), errors.New("not loaded")
				}
				return []byte("permission denied"), errors.New("print failed")
			case "bootstrap":
				return nil, errors.New("bootstrap failed")
			default:
				t.Fatalf("unexpected command %q", args[0])
			}
			return nil, nil
		})
		if err := s.Install(ctx, spec); err == nil {
			t.Fatal("bootstrap and recheck errors must surface")
		}
	})
}

func TestDarwinRestorePlistBranches(t *testing.T) {
	ctx := context.Background()

	t.Run("write old plist fails", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "agent.plist")
		oldWrite := writePlistFile
		writePlistFile = func(string, []byte, os.FileMode) error {
			return errors.New("write failed")
		}
		t.Cleanup(func() { writePlistFile = oldWrite })
		s := newDarwinScheduler(t.TempDir(), "501", nil)
		if err := s.restorePlist(ctx, path, []byte("old"), true, false); err == nil {
			t.Fatal("restore write failure must surface")
		}
	})

	t.Run("bootstrap restored plist fails", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "agent.plist")
		s := newDarwinScheduler(t.TempDir(), "501", func(
			context.Context, string, ...string,
		) ([]byte, error) {
			return nil, errors.New("bootstrap failed")
		})
		if err := s.restorePlist(ctx, path, []byte("old"), true, true); err == nil {
			t.Fatal("restore bootstrap failure must surface")
		}
	})

	t.Run("remove new plist fails", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "agent.plist")
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "keep"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		s := newDarwinScheduler(t.TempDir(), "501", nil)
		if err := s.restorePlist(ctx, path, nil, false, false); err == nil {
			t.Fatal("restore remove failure must surface")
		}
	})
}

func TestDarwinBootstrapAndRemoveBranches(t *testing.T) {
	ctx := context.Background()
	spec := Spec{Executable: "/tmp/qka", ConfigDir: "/tmp/q", IntervalMinutes: 30}

	t.Run("bootstrap already loaded", func(t *testing.T) {
		s := newDarwinScheduler(t.TempDir(), "501", func(
			context.Context, string, ...string,
		) ([]byte, error) {
			return []byte("service already loaded"), errors.New("already loaded")
		})
		if err := s.bootstrap(ctx, "/tmp/agent.plist"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("bootstrap generic error", func(t *testing.T) {
		s := newDarwinScheduler(t.TempDir(), "501", func(
			context.Context, string, ...string,
		) ([]byte, error) {
			return []byte("permission denied"), errors.New("bootstrap failed")
		})
		if err := s.bootstrap(ctx, "/tmp/agent.plist"); err == nil {
			t.Fatal("bootstrap failure must surface")
		}
	})

	t.Run("remove loaded check error", func(t *testing.T) {
		s := newDarwinScheduler(t.TempDir(), "501", func(
			context.Context, string, ...string,
		) ([]byte, error) {
			return []byte("permission denied"), errors.New("print failed")
		})
		if err := s.Remove(ctx, spec); err == nil {
			t.Fatal("remove loaded check error must surface")
		}
	})

	t.Run("remove bootout recheck error", func(t *testing.T) {
		printCalls := 0
		s := newDarwinScheduler(t.TempDir(), "501", func(
			_ context.Context, _ string, args ...string,
		) ([]byte, error) {
			switch args[0] {
			case "print":
				printCalls++
				if printCalls == 1 {
					return []byte("loaded"), nil
				}
				return []byte("permission denied"), errors.New("print failed")
			case "bootout":
				return nil, errors.New("bootout failed")
			default:
				t.Fatalf("unexpected command %q", args[0])
			}
			return nil, nil
		})
		if err := s.Remove(ctx, spec); err == nil {
			t.Fatal("remove bootout recheck error must surface")
		}
	})

	t.Run("remove service still loaded", func(t *testing.T) {
		s := newDarwinScheduler(t.TempDir(), "501", func(
			_ context.Context, _ string, args ...string,
		) ([]byte, error) {
			switch args[0] {
			case "print":
				return []byte("loaded"), nil
			case "bootout":
				return nil, errors.New("bootout failed")
			default:
				t.Fatalf("unexpected command %q", args[0])
			}
			return nil, nil
		})
		if err := s.Remove(ctx, spec); err == nil {
			t.Fatal("still-loaded service after bootout failure must surface")
		}
	})

	t.Run("status loaded check error", func(t *testing.T) {
		home := t.TempDir()
		s := newDarwinScheduler(home, "501", func(
			context.Context, string, ...string,
		) ([]byte, error) {
			return []byte("permission denied"), errors.New("print failed")
		})
		path := s.plistPath(spec)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("plist"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Status(ctx, spec); err == nil {
			t.Fatal("status loaded check error must surface")
		}
	})
}
