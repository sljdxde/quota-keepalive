//go:build !darwin && !windows

package autostart

import "errors"

var errUnsupported = errors.New("autostart: platform unsupported")

// Plist exists only on macOS.
func Plist(string, string) []byte { return nil }

// On reports that login startup is not implemented on this platform.
func On(string, string, string) error { return errUnsupported }

// Off reports that login startup is not implemented on this platform.
func Off(string, string) error { return errUnsupported }

// IsEnabled reports false on unsupported platforms.
func IsEnabled(string, string) bool { return false }
