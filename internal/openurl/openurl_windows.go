//go:build windows

package openurl

import "os/exec"

// Open opens raw through the shell URL handler. The URL is passed as a single
// argv entry; no shell command string is constructed.
func Open(raw string) error {
	if err := validate(raw); err != nil {
		return err
	}
	return exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", raw).Run()
}
