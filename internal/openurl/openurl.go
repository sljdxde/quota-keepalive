// Package openurl opens an already-validated web URL with the current user's
// default browser. Platform-specific code only handles the launch mechanism.
package openurl

import (
	"fmt"
	"net/url"
	"strings"
)

func validate(raw string) error {
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return fmt.Errorf("openurl: invalid URL: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "https", "http":
	default:
		return fmt.Errorf("openurl: unsupported scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("openurl: missing host")
	}
	return nil
}
