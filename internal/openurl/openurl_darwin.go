//go:build darwin

package openurl

import "os/exec"

// Open opens raw with macOS Launch Services.
func Open(raw string) error {
	if err := validate(raw); err != nil {
		return err
	}
	return exec.Command("open", raw).Run()
}
