package atomicfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Endpoint 是持久化到 endpoint.json 的实际 loopback 端点（只含展示/连接信息，无凭据）。
type Endpoint struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	Path string `json:"path"`
}

// URL 返回端点的完整 HTTP 地址。
func (e Endpoint) URL() string {
	return fmt.Sprintf("http://%s:%d%s", e.Host, e.Port, e.Path)
}

func endpointPath(configDir string) string {
	return filepath.Join(configDir, "endpoint.json")
}

// SaveEndpoint 把端点原子写入 <configDir>/endpoint.json（0600），并确保配置目录为 0700。
func SaveEndpoint(configDir string, ep Endpoint) error {
	if err := EnsureDir(configDir, 0o700); err != nil {
		return err
	}
	data, err := marshalIndent(ep, "", "  ")
	if err != nil {
		return err
	}
	return WriteFile(endpointPath(configDir), data, 0o600)
}

// LoadEndpoint 从 <configDir>/endpoint.json 读回端点；文件不存在时返回 fs.ErrNotExist。
func LoadEndpoint(configDir string) (Endpoint, error) {
	data, err := os.ReadFile(endpointPath(configDir))
	if err != nil {
		return Endpoint{}, err
	}
	var ep Endpoint
	if err := json.Unmarshal(data, &ep); err != nil {
		return Endpoint{}, err
	}
	return ep, nil
}
