package cli

import (
	"os"
	"os/exec"
	"path/filepath"

	"qka/internal/portsel"
)

var (
	workingDir = os.Getwd
	lookPath   = exec.LookPath
)

// defaultPort 是 serve 未指定端口时的监听端口（与 portsel 一致）。
const defaultPort = portsel.DefaultPort

// resolveAdaptersRoot 返回适配器父目录：显式覆盖 > 环境变量 > 可执行文件同级 > cwd。
func resolveAdaptersRoot(env *Env, override string) string {
	if override != "" {
		return override
	}
	if root := env.Getenv("QKA_ADAPTERS_DIR"); root != "" {
		return root
	}
	if exe, err := executablePath(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), "adapters")
		if isDir(cand) {
			return cand
		}
	}
	if cwd, err := workingDir(); err == nil {
		cand := filepath.Join(cwd, "adapters")
		if isDir(cand) {
			return cand
		}
	}
	return "adapters"
}

// resolveBun 返回 Bun 路径：显式覆盖 > 环境变量 > 可执行文件同级 > PATH。
func resolveBun(env *Env, override string) string {
	if override != "" {
		return override
	}
	if p := env.Getenv("QKA_BUN"); p != "" {
		return p
	}
	if exe, err := executablePath(); err == nil {
		if bun := siblingBun(filepath.Dir(exe)); bun != "" {
			return bun
		}
	}
	if p, err := lookPath("bun"); err == nil {
		return p
	}
	return "bun"
}

// resolveProviderPluginsRoot finds the bundled OpenCode provider plugins.
// QKA_PROVIDER_PLUGINS_DIR may point at a development copy.
func resolveProviderPluginsRoot(env *Env, override string) string {
	if override != "" {
		return override
	}
	if root := env.Getenv("QKA_PROVIDER_PLUGINS_DIR"); root != "" {
		return root
	}
	if exe, err := executablePath(); err == nil {
		base := filepath.Dir(exe)
		for _, candidate := range []string{
			filepath.Join(base, "provider-plugins"),
			filepath.Join(base, "..", "Resources", "provider-plugins"),
		} {
			if isDir(candidate) {
				return filepath.Clean(candidate)
			}
		}
	}
	if cwd, err := workingDir(); err == nil {
		candidate := filepath.Join(cwd, "provider-plugins")
		if isDir(candidate) {
			return candidate
		}
	}
	return ""
}

func siblingBun(dir string) string {
	for _, name := range []string{"bun", "bun.exe"} {
		candidate := filepath.Join(dir, name)
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
