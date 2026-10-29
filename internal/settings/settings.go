package settings

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"qka/internal/atomicfile"
)

// FileName 是设置文件名。
const FileName = "settings.json"

var (
	// ErrUnknownKey 表示设置项不在 schema 中。
	ErrUnknownKey = errors.New("settings: unknown key")
)

// Settings 持有当前设置值。
type Settings struct {
	values map[string]bool
}

// Default 返回所有字段为最保守默认值的设置。
func Default() *Settings {
	s := &Settings{values: make(map[string]bool, len(Schema))}
	for _, f := range Schema {
		s.values[f.Key] = f.Default
	}
	return s
}

func field(key string) *Field {
	for i := range Schema {
		if Schema[i].Key == key {
			return &Schema[i]
		}
	}
	return nil
}

// Get 返回设置项当前值（未知 key 返回零值）。
func (s *Settings) Get(key string) bool {
	if field(key) == nil {
		return false
	}
	return s.values[key]
}

// Set 设置一个字段；未知 key 被 schema 校验拒绝。
func (s *Settings) Set(key string, value bool) error {
	if field(key) == nil {
		return ErrUnknownKey
	}
	s.values[key] = value
	return nil
}

// Values 返回所有 schema 字段的当前值（深拷贝）。
func (s *Settings) Values() map[string]bool {
	out := make(map[string]bool, len(Schema))
	for _, f := range Schema {
		out[f.Key] = s.values[f.Key]
	}
	return out
}

// validate 校验所有值对应的 key 均在 schema 中。
func (s *Settings) validate() error {
	for key := range s.values {
		if field(key) == nil {
			return ErrUnknownKey
		}
	}
	return nil
}

func (s *Settings) marshal() ([]byte, error) {
	return json.MarshalIndent(s.values, "", "  ")
}

// Save 在进程间锁下做 schema 校验、备份旧文件并原子替换。
func (s *Settings) Save(configDir string) error {
	if err := s.validate(); err != nil {
		return err
	}
	return withConfigLock(configDir, func() error {
		path := filepath.Join(configDir, FileName)
		// 备份现有文件。
		if data, err := os.ReadFile(path); err == nil {
			_ = atomicfile.WriteFile(path+".bak", data, 0o600)
		}
		data, err := s.marshal()
		if err != nil {
			return err
		}
		return atomicfile.WriteFile(path, data, 0o600)
	})
}

// Load 读取设置；文件缺失时返回默认值。
func Load(configDir string) (*Settings, error) {
	path := filepath.Join(configDir, FileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return nil, err
	}
	var values map[string]bool
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, err
	}
	s := Default()
	for key, value := range values {
		// 仅接受 schema 中存在的 key；未知项忽略。
		if field(key) != nil {
			s.values[key] = value
		}
	}
	return s, nil
}
