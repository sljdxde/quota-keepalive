package atomicfile

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// snapshotableFiles 是允许进入恢复快照的非凭据状态文件白名单。
// 凭据只存于 Keychain 或受保护位置，永远不进入快照，故快照可安全用于恢复。
var snapshotableFiles = []string{"endpoint.json"}

// Snapshot 是可恢复的非凭据状态集合，键为相对配置目录的文件名。
type Snapshot struct {
	CreatedAt time.Time         `json:"created_at"`
	Files     map[string][]byte `json:"files"`
}

func snapshotsDir(configDir string) string {
	return filepath.Join(configDir, "snapshots")
}

// TakeSnapshot 读取白名单内存在的状态文件构造快照；不存在的文件跳过。
func TakeSnapshot(configDir string, now time.Time) (Snapshot, error) {
	files := make(map[string][]byte)
	for _, name := range snapshotableFiles {
		data, err := os.ReadFile(filepath.Join(configDir, name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return Snapshot{}, err
		}
		files[name] = data
	}
	return Snapshot{CreatedAt: now, Files: files}, nil
}

// Save 把快照原子写入 <configDir>/snapshots/snapshot-<unix>.json（0600）。
func (s Snapshot) Save(configDir string) error {
	dir := snapshotsDir(configDir)
	if err := EnsureDir(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	file := filepath.Join(dir, "snapshot-"+strconv.FormatInt(s.CreatedAt.Unix(), 10)+".json")
	return WriteFile(file, data, 0o600)
}

// RestoreLatestSnapshot 读取最近一个快照，把其中文件原子还原到配置目录（0600）。
func RestoreLatestSnapshot(configDir string) error {
	dir := snapshotsDir(configDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return fs.ErrNotExist
	}
	sort.Strings(names)
	latest := names[len(names)-1]

	data, err := os.ReadFile(filepath.Join(dir, latest))
	if err != nil {
		return err
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return err
	}

	for name, content := range snap.Files {
		if err := WriteFile(filepath.Join(configDir, name), content, 0o600); err != nil {
			return err
		}
	}
	return nil
}
