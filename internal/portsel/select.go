// Package portsel 负责首装时的 loopback 端口选择与端点持久化。
package portsel

import (
	"errors"

	"qka/internal/atomicfile"
)

const (
	// Host 是唯一允许绑定的地址：loopback。
	Host = "127.0.0.1"
	// DefaultPort 是默认端点端口。
	DefaultPort = 43189
	// MinPort/MaxPort 界定首装可选择的端口范围。
	MinPort = 43189
	MaxPort = 43299
	// Path 是 MCP 端点路径。
	Path = "/mcp"
)

// ErrNoFreePort 表示范围内没有可用端口。
var ErrNoFreePort = errors.New("端口范围内无可用端口")

// Select 在默认端口空闲时选默认，否则顺序选择 [MinPort, MaxPort] 中第一个可用端口。
// isFree 用于判断端口是否可绑定，便于注入测试。
func Select(isFree func(port int) bool) (atomicfile.Endpoint, error) {
	for port := MinPort; port <= MaxPort; port++ {
		if isFree(port) {
			return atomicfile.Endpoint{Host: Host, Port: port, Path: Path}, nil
		}
	}
	return atomicfile.Endpoint{}, ErrNoFreePort
}
