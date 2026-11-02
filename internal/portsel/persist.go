package portsel

import (
	"errors"
	"fmt"
	"io/fs"

	"qka/internal/atomicfile"
)

// ErrEndpointConflict 表示已持久化（已配对）的端点端口被占用。
// 这是需要宿主侧重新配置的显式错误，QKA 不会静默改端口或杀占用进程。
var ErrEndpointConflict = errors.New("已持久化端点端口被占用，需在宿主侧重新配置")

var saveEndpoint = atomicfile.SaveEndpoint

// SelectAndPersist 为首装选择端点并持久化到 endpoint.json。
// 若端点已存在：端口仍可用则原样返回（重启读回同一端口）；端口被占则返回
// ErrEndpointConflict。isFree 用于判断端口是否可绑定，便于注入测试。
func SelectAndPersist(configDir string, isFree func(int) bool) (atomicfile.Endpoint, error) {
	if existing, err := atomicfile.LoadEndpoint(configDir); err == nil {
		if isFree(existing.Port) {
			return existing, nil
		}
		return atomicfile.Endpoint{}, fmt.Errorf("%w: %s:%d",
			ErrEndpointConflict, existing.Host, existing.Port)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return atomicfile.Endpoint{}, err
	}

	ep, err := Select(isFree)
	if err != nil {
		return ep, err
	}
	if err := saveEndpoint(configDir, ep); err != nil {
		return ep, err
	}
	return ep, nil
}
