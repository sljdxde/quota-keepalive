package cli

import (
	"os"
	"os/exec"
	"path/filepath"

	"qka/internal/portsel"
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
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), "adapters")
		if isDir(cand) {
			return cand
		}
	}
	if cwd, err := os.Getwd(); err == nil {
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
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), "bun")
		if fileExists(cand) {
			return cand
		}
	}
	if p, err := exec.LookPath("bun"); err == nil {
		return p
	}
	return "bun"
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
