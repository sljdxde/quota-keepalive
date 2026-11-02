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
	// ErrWrongType 表示设置值类型与 schema 不符。
	ErrWrongType = errors.New("settings: wrong value type")
	// ErrInvalidOption 表示 select 值不在允许选项中。
	ErrInvalidOption = errors.New("settings: invalid option")
)

var marshalSettings = json.MarshalIndent

// Settings 持有当前设置值。
type Settings struct {
	values map[string]any
}

// Default 返回所有字段为最保守默认值的设置。
func Default() *Settings {
	s := &Settings{values: make(map[string]any, len(Schema))}
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
	f := field(key)
	if f == nil || f.Type != FieldBool {
		return false
	}
	value, _ := s.values[key].(bool)
	return value
}

// GetString 返回 select/text 设置项当前值（未知或类型不符返回空字符串）。
func (s *Settings) GetString(key string) string {
	f := field(key)
	if f == nil || (f.Type != FieldSelect && f.Type != FieldText) {
		return ""
	}
	value, _ := s.values[key].(string)
	return value
}

// Set 设置一个字段；未知 key 被 schema 校验拒绝。
func (s *Settings) Set(key string, value bool) error {
	f := field(key)
	if f == nil {
		return ErrUnknownKey
	}
	if f.Type != FieldBool {
		return ErrWrongType
	}
	s.values[key] = value
	return nil
}

// SetString 设置一个 select/text 字段；未知 key、类型不符或非法值均被拒绝。
func (s *Settings) SetString(key, value string) error {
	f := field(key)
	if f == nil {
		return ErrUnknownKey
	}
	switch f.Type {
	case FieldText:
		if len(value) > maxTextValueBytes {
			return ErrInvalidOption
		}
		s.values[key] = value
		return nil
	case FieldSelect:
		for _, option := range f.Options {
			if option.Value == value {
				s.values[key] = value
				return nil
			}
		}
		return ErrInvalidOption
	default:
		return ErrWrongType
	}
}

// maxTextValueBytes 限制自由文本设置项的长度，避免把超长内容塞进配置文件。
const maxTextValueBytes = 512

// Values 返回所有 schema 字段的当前值（深拷贝）。
func (s *Settings) Values() map[string]any {
	out := make(map[string]any, len(Schema))
	for _, f := range Schema {
		out[f.Key] = s.values[f.Key]
	}
	return out
}

// validate 校验所有值对应的 key 均在 schema 中。
func (s *Settings) validate() error {
	for key, value := range s.values {
		f := field(key)
		if f == nil {
			return ErrUnknownKey
		}
		switch f.Type {
		case FieldBool:
			if _, ok := value.(bool); !ok {
				return ErrWrongType
			}
		case FieldSelect:
			text, ok := value.(string)
			if !ok {
				return ErrWrongType
			}
			valid := false
			for _, option := range f.Options {
				if option.Value == text {
					valid = true
					break
				}
			}
			if !valid {
				return ErrInvalidOption
			}
		case FieldText:
			text, ok := value.(string)
			if !ok {
				return ErrWrongType
			}
			if len(text) > maxTextValueBytes {
				return ErrInvalidOption
			}
		default:
			return ErrWrongType
		}
	}
	return nil
}

func (s *Settings) marshal() ([]byte, error) {
	return marshalSettings(s.values, "", "  ")
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
	var values map[string]any
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, err
	}
	s := Default()
	for key, value := range values {
		// 仅接受 schema 中存在的 key；未知项忽略。
		if field(key) != nil {
			s.values[key] = normalizeRetiredValue(key, value)
		}
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	return s, nil
}

// normalizeRetiredValue keeps old on-disk choices usable when a select catalog
// changes. Unknown values still flow through normal schema validation.
func normalizeRetiredValue(key string, value any) any {
	if key != "title_theme" {
		return value
	}
	theme, ok := value.(string)
	if !ok {
		return value
	}
	switch theme {
	case "wind", "cultivation", "wuxia", "mage", "gameRank", "engineer":
		return "vehicles"
	default:
		return value
	}
}
