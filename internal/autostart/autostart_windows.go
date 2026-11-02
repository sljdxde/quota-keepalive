//go:build windows

package autostart

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

const (
	runKey  = `Software\Microsoft\Windows\CurrentVersion\Run`
	runName = "QKA"
)

// On writes the current user's HKCU Run value. Windows ignores homeDir; the
// parameter keeps the cross-platform API symmetric with macOS.
func On(_ string, programPath, _ string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	// Windows file names cannot contain a double quote, so wrapping the path
	// is sufficient and avoids Go string escaping leaking doubled backslashes.
	return key.SetStringValue(runName, `"`+programPath+`" `+Arg)
}

// Off removes the QKA Run value. A missing key or value is already off.
func Off(_ string, _ string) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer key.Close()
	if err := key.DeleteValue(runName); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// IsEnabled reports whether the current user's QKA Run value exists.
func IsEnabled(_ string, _ string) bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	_, _, err = key.GetStringValue(runName)
	return err == nil
}
