package portsel

import (
	"fmt"
	"net"
)

// PortFree 通过尝试在 loopback 上绑定来判断端口是否可用；
// 只做探测并立即释放，绝不影响占用者。
func PortFree(port int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf("%s:%d", Host, port))
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}
