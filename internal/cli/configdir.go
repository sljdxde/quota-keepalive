package cli

import "path/filepath"

// ResolveConfigDir 返回当前生效的配置目录：QKA_CONFIG_DIR 优先，否则 HOME 下 .qka。
func ResolveConfigDir(getenv func(string) string) string {
	if override := getenv("QKA_CONFIG_DIR"); override != "" {
		return override
	}
	return filepath.Join(getenv("HOME"), ".qka")
}
