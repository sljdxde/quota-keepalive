package cli

import "testing"

type fakeFS struct {
	exists map[string]bool
	files  map[string]string
}

func (f fakeFS) Exists(p string) bool        { return f.exists[p] }
func (f fakeFS) ReadFile(p string) (string, bool) { s, ok := f.files[p]; return s, ok }

func TestDetectContainerFalseOnBareMac(t *testing.T) {
	fs := fakeFS{exists: map[string]bool{}, files: map[string]string{}}
	if DetectContainer(fs, func(string) string { return "" }) {
		t.Error("裸机不应被判为容器")
	}
}

func TestDetectContainerTrueWhenDockerEnvPresent(t *testing.T) {
	fs := fakeFS{exists: map[string]bool{"/.dockerenv": true}, files: map[string]string{}}
	if !DetectContainer(fs, func(string) string { return "" }) {
		t.Error("存在 /.dockerenv 应判为容器")
	}
}

func TestDetectContainerTrueWhenCgroupMentionsContainer(t *testing.T) {
	fs := fakeFS{files: map[string]string{"/proc/1/cgroup": "11:cpuset:/docker/abc\n"}}
	if !DetectContainer(fs, func(string) string { return "" }) {
		t.Error("cgroup 含 docker 应判为容器")
	}
}

func TestDetectContainerTrueWhenKubernetesEnv(t *testing.T) {
	fs := fakeFS{exists: map[string]bool{}, files: map[string]string{}}
	getenv := func(k string) string {
		if k == "KUBERNETES_SERVICE_HOST" {
			return "10.0.0.1"
		}
		return ""
	}
	if !DetectContainer(fs, getenv) {
		t.Error("KUBERNETES_SERVICE_HOST 存在应判为容器")
	}
}
