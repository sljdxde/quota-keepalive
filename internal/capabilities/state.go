package capabilities

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"unicode"

	"qka/internal/atomicfile"
)

// ErrUnsafeNamespace 表示命名空间或键含路径穿越等非法字符。
var ErrUnsafeNamespace = errors.New("命名空间或键含非法字符，拒绝访问")

// Namespace 是适配器私有状态/日志的作用域：目标 × 账号 × 操作。
type Namespace struct {
	Target    string
	AccountID string
	Operation string
}

// safeComponent 仅允许字母、数字、-、_、.，杜绝路径分隔符与 ".." 穿越。
func safeComponent(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for _, r := range s {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

// StateService 提供按命名空间隔离的私有状态读写。
type StateService struct {
	rootDir string
	grant   Grant
}

// NewStateService 组装状态服务；数据落在 <configDir>/adapter-state/。
func NewStateService(configDir string, grant Grant) *StateService {
	return &StateService{rootDir: filepath.Join(configDir, "adapter-state"), grant: grant}
}

func (s *StateService) dir(ns Namespace) (string, error) {
	for _, c := range []string{ns.Target, ns.AccountID, ns.Operation} {
		if !safeComponent(c) {
			return "", ErrUnsafeNamespace
		}
	}
	return filepath.Join(s.rootDir, ns.Target, ns.AccountID, ns.Operation), nil
}

// Set 在自身命名空间内写入一个键（0600，原子替换）。
func (s *StateService) Set(ctx context.Context, ns Namespace, key string, value []byte) error {
	if err := s.grant.Authorize("state.set"); err != nil {
		return err
	}
	dir, err := s.dir(ns)
	if err != nil {
		return err
	}
	if !safeComponent(key) {
		return ErrUnsafeNamespace
	}
	if err := atomicfile.EnsureDir(dir, 0o700); err != nil {
		return err
	}
	return atomicfile.WriteFile(filepath.Join(dir, key+".json"), value, 0o600)
}

// Get 读取自身命名空间内的一个键；越界或不存在返回对应错误。
func (s *StateService) Get(ctx context.Context, ns Namespace, key string) ([]byte, error) {
	if err := s.grant.Authorize("state.get"); err != nil {
		return nil, err
	}
	dir, err := s.dir(ns)
	if err != nil {
		return nil, err
	}
	if !safeComponent(key) {
		return nil, ErrUnsafeNamespace
	}
	return os.ReadFile(filepath.Join(dir, key+".json"))
}
