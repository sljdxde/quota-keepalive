//go:build !darwin && !linux

package settings

// withConfigLock 在不支持 flock 的平台退化为直接执行（写入仍由原子替换保证）。
func withConfigLock(_ string, fn func() error) error {
	return fn()
}
