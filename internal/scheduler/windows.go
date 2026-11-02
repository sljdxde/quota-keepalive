//go:build windows

package scheduler

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"time"
	"unicode/utf16"
)

type commandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

type windowsScheduler struct {
	run    commandRunner
	now    func() time.Time
	userID string
}

func realRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// New returns the current-user Task Scheduler backend.
func New() Scheduler {
	userID := ""
	if current, err := user.Current(); err == nil {
		userID = current.Username
	}
	return &windowsScheduler{run: realRunner, now: time.Now, userID: userID}
}

func newWindowsScheduler(run commandRunner, now func() time.Time) *windowsScheduler {
	return &windowsScheduler{run: run, now: now}
}

func encodeUTF16LE(data []byte) []byte {
	units := utf16.Encode([]rune(string(data)))
	out := make([]byte, 2, 2+len(units)*2)
	out[0], out[1] = 0xff, 0xfe
	for _, unit := range units {
		var buf [2]byte
		binary.LittleEndian.PutUint16(buf[:], unit)
		out = append(out, buf[:]...)
	}
	return out
}

func (s *windowsScheduler) Install(ctx context.Context, spec Spec) error {
	data, err := WindowsTaskXML(spec, s.now(), s.userID)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "qka-task-*.xml")
	if err != nil {
		return err
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err := f.Write(encodeUTF16LE(data)); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	out, err := s.run(ctx, "schtasks", "/Create", "/TN",
		WindowsTaskName(spec.ConfigDir), "/XML", path, "/F")
	if err != nil {
		return fmt.Errorf("scheduler: schtasks create: %w (%s)",
			err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (s *windowsScheduler) Remove(ctx context.Context, spec Spec) error {
	name := WindowsTaskName(spec.ConfigDir)
	out, err := s.run(ctx, "schtasks", "/Delete", "/TN", name, "/F")
	if err != nil && !taskMissing(out) {
		return fmt.Errorf("scheduler: schtasks delete: %w (%s)",
			err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (s *windowsScheduler) Status(ctx context.Context, spec Spec) (Status, error) {
	out, err := s.run(ctx, "schtasks", "/Query", "/TN", WindowsTaskName(spec.ConfigDir))
	if err != nil {
		if !taskMissing(out) {
			return Status{}, fmt.Errorf("scheduler: schtasks query: %w (%s)",
				err, strings.TrimSpace(string(out)))
		}
		return Status{
			Installed:   false,
			PowerPolicy: "当前用户登录时运行；允许电池供电并支持睡眠唤醒后补跑",
		}, nil
	}
	return Status{
		Installed:   true,
		PowerPolicy: "当前用户登录时运行；允许电池供电并支持睡眠唤醒后补跑",
	}, nil
}

func taskMissing(output []byte) bool {
	text := strings.ToLower(string(output))
	for _, marker := range []string{
		"cannot find", "does not exist", "not found", "找不到", "不存在",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}
