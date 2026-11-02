//go:build !windows

package portsel

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunLsofBranches(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"no-match", "#!/bin/sh\nexit 1\n"},
		{"empty", "#!/bin/sh\nprintf ' \\n'\n"},
		{"failure", "#!/bin/sh\nexit 2\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "lsof")
			if err := os.WriteFile(script, []byte(tc.body), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			lines, err := runLsof(43189)
			switch tc.name {
			case "failure":
				if err == nil {
					t.Fatal("非 1 退出码应返回错误")
				}
			default:
				if err != nil || len(lines) != 0 {
					t.Fatalf("应返回空占用列表，实际 %v %v", lines, err)
				}
			}
		})
	}
}
