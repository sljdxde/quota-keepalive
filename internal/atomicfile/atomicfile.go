// Package atomicfile 提供配置目录下私有状态文件的原子写与目录收敛原语。
// 所有状态文件都应 owner-only，写入走 tmp+rename，避免崩溃产生半文件。
package atomicfile

import (
	"io/fs"
	"os"
	"path/filepath"
)

// EnsureDir 创建目录（含必要父级），并把最终目录权限收敛为 perm。
// owner-only 场景传 0o700：MkdirAll 受 umask 影响且不改动已存在目录，故显式 Chmod。
func EnsureDir(dir string, perm fs.FileMode) error {
	if err := os.MkdirAll(dir, perm); err != nil {
		return err
	}
	return os.Chmod(dir, perm)
}

// WriteFile 在目标文件同目录创建临时文件，写入并 fsync 后 rename 原子替换。
// 最终文件权限收敛为 perm（owner-only 场景传 0o600）。调用方须先确保父目录存在。
// 任何失败路径都清理临时文件，绝不留下半文件。
func WriteFile(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	// 先收敛权限，再写数据，避免内容在过宽权限窗口内落盘。
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	committed = true
	return nil
}
