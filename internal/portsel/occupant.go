package portsel

import (
	"io/fs"
	"os/exec"
	"strconv"
	"strings"
)

// Occupant 报告占用某端口的进程身份。qka 只报告、绝不结束占用进程。
type Occupant struct {
	Port int    `json:"port"`
	PID  int    `json:"pid"`
	Name string `json:"name"`
	Kind string `json:"kind"` // docker | orbstack | other
}

// runLsof 以机器可解析格式（-F p/c）读取端口上的 LISTEN 进程。
func runLsof(port int) ([]string, error) {
	out, err := exec.Command("lsof", "-nP", "-Fpct",
		"-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN").Output()
	if err != nil {
		// lsof 无匹配时退出码为 1：视为无占用者，而非命令失败。
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

func describeOccupant(port int, lsof func(int) ([]string, error)) (Occupant, error) {
	lines, err := lsof(port)
	if err != nil {
		return Occupant{}, err
	}

	var pid int
	var name string
	found := false
	for _, line := range lines {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			if v, err := strconv.Atoi(line[1:]); err == nil {
				pid = v
			}
		case 'c':
			if !found {
				name = line[1:]
				found = true
			}
		}
	}

	if !found {
		return Occupant{}, fs.ErrNotExist
	}
	return Occupant{Port: port, PID: pid, Name: name, Kind: classify(name)}, nil
}

func classify(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "orbstack"):
		return "orbstack"
	case strings.Contains(lower, "docker"):
		return "docker"
	default:
		return "other"
	}
}

// DescribeOccupant 识别端口上的实际占用进程（含 Docker/OrbStack 转发器），不杀进程。
func DescribeOccupant(port int) (Occupant, error) {
	return describeOccupant(port, runLsof)
}
