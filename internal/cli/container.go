package cli

import "strings"

// FS 是容器检测所需的最小文件系统视图。
type FS interface {
	Exists(path string) bool
	ReadFile(path string) (string, bool)
}

// DetectContainer 判定当前是否运行在容器中：/.dockerenv、cgroup 标记或 k8s 环境变量。
func DetectContainer(fs FS, getenv func(string) string) bool {
	if fs.Exists("/.dockerenv") {
		return true
	}
	if getenv("KUBERNETES_SERVICE_HOST") != "" {
		return true
	}
	if content, ok := fs.ReadFile("/proc/1/cgroup"); ok {
		for _, marker := range []string{"docker", "containerd", "kubepods", "lxc"} {
			if strings.Contains(content, marker) {
				return true
			}
		}
	}
	return false
}
