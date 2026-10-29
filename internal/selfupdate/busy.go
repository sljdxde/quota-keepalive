package selfupdate

import (
	"context"
	"path/filepath"
	"time"
)

// WaitIdle 忙时等待：轮询 idle，直到空闲或 ctx 超时。
func WaitIdle(ctx context.Context, idle func() bool, interval time.Duration) error {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	if idle() {
		return nil
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if idle() {
				return nil
			}
		}
	}
}

// RollbackPath 返回旧版保留路径；新版首启/健康失败时自动回滚。
func RollbackPath(installDir string) string {
	return filepath.Join(installDir, "qka.old")
}
