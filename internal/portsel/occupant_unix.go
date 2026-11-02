//go:build !windows

package portsel

import (
	"os/exec"
	"strconv"
	"strings"
)

// runLsof reads the LISTEN process in a machine-readable format. On Unix-like
// systems lsof is the least invasive way to identify an occupant.
func runLsof(port int) ([]string, error) {
	out, err := exec.Command("lsof", "-nP", "-Fpct",
		"-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN").Output()
	if err != nil {
		// lsof exits 1 when there is no match.
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return []string{}, nil
		}
		return nil, err
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return []string{}, nil
	}
	return strings.Split(trimmed, "\n"), nil
}
