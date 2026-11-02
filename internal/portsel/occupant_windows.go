//go:build windows

package portsel

import (
	"os/exec"
	"strconv"
	"strings"
)

// runLsof is named for the shared parser; on Windows netstat supplies the
// listening PID without requiring lsof or administrator privileges.
func runLsof(port int) ([]string, error) {
	out, err := exec.Command("netstat", "-ano", "-p", "tcp").Output()
	if err != nil {
		return nil, err
	}
	suffix := ":" + strconv.Itoa(port)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || !strings.EqualFold(fields[3], "LISTENING") ||
			!strings.HasSuffix(fields[1], suffix) {
			continue
		}
		pid, err := strconv.Atoi(fields[4])
		if err != nil {
			continue
		}
		name := fmtPIDName(pid)
		return []string{"p" + strconv.Itoa(pid), "c" + name}, nil
	}
	return []string{}, nil
}

func fmtPIDName(pid int) string {
	out, err := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid),
		"/FO", "CSV", "/NH").Output()
	if err != nil {
		return "pid " + strconv.Itoa(pid)
	}
	line := strings.TrimSpace(string(out))
	if line == "" || strings.HasPrefix(line, "INFO:") {
		return "pid " + strconv.Itoa(pid)
	}
	// CSV first field is the image name.
	name := strings.Trim(strings.SplitN(line, ",", 2)[0], `"`)
	if name == "" {
		return "pid " + strconv.Itoa(pid)
	}
	return name
}
