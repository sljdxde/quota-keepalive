//go:build darwin

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"qka/internal/atomicfile"
)

type commandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

var writePlistFile = atomicfile.WriteFile

type darwinScheduler struct {
	home string
	uid  string
	run  commandRunner
}

func realRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// New returns the current-user LaunchAgent scheduler.
func New() Scheduler {
	home, _ := os.UserHomeDir()
	return &darwinScheduler{
		home: home,
		uid:  fmt.Sprintf("%d", os.Getuid()),
		run:  realRunner,
	}
}

func newDarwinScheduler(home, uid string, run commandRunner) *darwinScheduler {
	return &darwinScheduler{home: home, uid: uid, run: run}
}

func (s *darwinScheduler) plistPath(spec Spec) string {
	return filepath.Join(s.home, "Library", "LaunchAgents", LaunchdLabel(spec.ConfigDir)+".plist")
}

func (s *darwinScheduler) Install(ctx context.Context, spec Spec) error {
	if !executableOK(spec.Executable) {
		return fmt.Errorf("scheduler: invalid executable")
	}
	data, err := LaunchdPlist(spec)
	if err != nil {
		return err
	}
	path := s.plistPath(spec)
	if err := atomicfile.EnsureDir(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	label := LaunchdLabel(spec.ConfigDir)
	old, oldErr := os.ReadFile(path)
	hadOld := oldErr == nil
	if oldErr != nil && !os.IsNotExist(oldErr) {
		return oldErr
	}
	wasLoaded, err := s.loaded(ctx, label)
	if err != nil {
		return err
	}
	if wasLoaded {
		if _, err := s.run(ctx, "launchctl", "bootout", "gui/"+s.uid+"/"+label); err != nil {
			stillLoaded, checkErr := s.loaded(ctx, label)
			if checkErr != nil {
				return errors.Join(fmt.Errorf("scheduler: launchctl bootout: %w", err), checkErr)
			}
			if stillLoaded {
				return fmt.Errorf("scheduler: launchctl bootout: %w", err)
			}
		}
	}
	if err := writePlistFile(path, data, 0o644); err != nil {
		if wasLoaded {
			_ = s.bootstrap(ctx, path)
		}
		return err
	}
	out, err := s.run(ctx, "launchctl", "bootstrap", "gui/"+s.uid, path)
	if err != nil {
		loaded, checkErr := s.loaded(ctx, label)
		if checkErr != nil || !loaded {
			restoreErr := s.restorePlist(ctx, path, old, hadOld, wasLoaded)
			primary := fmt.Errorf("scheduler: launchctl bootstrap: %w (%s)",
				err, strings.TrimSpace(string(out)))
			if checkErr != nil {
				return errors.Join(primary, checkErr, restoreErr)
			}
			return errors.Join(primary, restoreErr)
		}
	}
	return nil
}

func (s *darwinScheduler) restorePlist(
	ctx context.Context,
	path string,
	old []byte,
	hadOld, wasLoaded bool,
) error {
	var errs []error
	if hadOld {
		if err := writePlistFile(path, old, 0o644); err != nil {
			errs = append(errs, err)
		} else if wasLoaded {
			if err := s.bootstrap(ctx, path); err != nil {
				errs = append(errs, err)
			}
		}
	} else if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (s *darwinScheduler) bootstrap(ctx context.Context, path string) error {
	out, err := s.run(ctx, "launchctl", "bootstrap", "gui/"+s.uid, path)
	if err == nil || alreadyLoaded(out) {
		return nil
	}
	return fmt.Errorf("scheduler: launchctl bootstrap: %w", err)
}

func (s *darwinScheduler) loaded(ctx context.Context, label string) (bool, error) {
	out, err := s.run(ctx, "launchctl", "print", "gui/"+s.uid+"/"+label)
	if err == nil {
		return true, nil
	}
	if launchdMissing(out) {
		return false, nil
	}
	return false, fmt.Errorf("scheduler: launchctl print: %w", err)
}

func launchdMissing(output []byte) bool {
	text := strings.ToLower(string(output))
	for _, marker := range []string{
		"could not find service", "service not found", "no such process",
		"not loaded", "not found",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func alreadyLoaded(output []byte) bool {
	text := strings.ToLower(string(output))
	return strings.Contains(text, "already loaded") ||
		strings.Contains(text, "already bootstrapped") ||
		strings.Contains(text, "service already loaded")
}

func (s *darwinScheduler) Remove(ctx context.Context, spec Spec) error {
	label := LaunchdLabel(spec.ConfigDir)
	loaded, err := s.loaded(ctx, label)
	if err != nil {
		return err
	}
	if loaded {
		if _, err := s.run(ctx, "launchctl", "bootout", "gui/"+s.uid+"/"+label); err != nil {
			stillLoaded, checkErr := s.loaded(ctx, label)
			if checkErr != nil {
				return errors.Join(fmt.Errorf("scheduler: launchctl bootout: %w", err), checkErr)
			}
			if stillLoaded {
				return fmt.Errorf("scheduler: launchctl bootout: %w", err)
			}
		}
	}
	err = os.Remove(s.plistPath(spec))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *darwinScheduler) Status(ctx context.Context, spec Spec) (Status, error) {
	_, err := os.Stat(s.plistPath(spec))
	if os.IsNotExist(err) {
		return Status{Installed: false, PowerPolicy: "当前用户登录后运行"}, nil
	}
	if err != nil {
		return Status{}, err
	}
	loaded, err := s.loaded(ctx, LaunchdLabel(spec.ConfigDir))
	if err != nil {
		return Status{}, err
	}
	return Status{Installed: loaded, PowerPolicy: "当前用户登录后运行"}, nil
}
