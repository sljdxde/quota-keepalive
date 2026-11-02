//go:build !darwin && !windows

package openurl

import (
	"errors"
	"os/exec"
	"runtime"
)

// Open uses xdg-open on supported Unix desktops.
func Open(raw string) error {
	if err := validate(raw); err != nil {
		return err
	}
	if runtime.GOOS != "linux" {
		return errors.New("openurl: unsupported platform")
	}
	return exec.Command("xdg-open", raw).Run()
}
