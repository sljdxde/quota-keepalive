package gui

import (
	"strings"
	"testing"
)

func TestKeepaliveIsHiddenFromAllProductSurfaces(t *testing.T) {
	for _, path := range []string{
		"frontend/main.html",
		"frontend/settings.html",
		"frontend/panel.html",
		"frontend/assets/panel.js",
	} {
		data, err := frontendFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "保活") {
			t.Errorf("%s must not expose keepalive copy", path)
		}
	}
}
