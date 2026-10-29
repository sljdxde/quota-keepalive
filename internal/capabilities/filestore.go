package capabilities

import (
	"context"
	"os"
	"path/filepath"

	"qka/internal/atomicfile"
)

// FileSecretStore 是凭据的受保护文件回退：<configDir>/secrets/，0600 文件、0700 目录。
type FileSecretStore struct {
	rootDir string
}

// NewFileSecretStore 组装文件凭据后端。
func NewFileSecretStore(configDir string) *FileSecretStore {
	return &FileSecretStore{rootDir: filepath.Join(configDir, "secrets")}
}

func (f *FileSecretStore) path(service, account string) string {
	return filepath.Join(f.rootDir, service, account+".secret")
}

func (f *FileSecretStore) Set(_ context.Context, service, account, secret string) error {
	p := f.path(service, account)
	if err := atomicfile.EnsureDir(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return atomicfile.WriteFile(p, []byte(secret), 0o600)
}

func (f *FileSecretStore) Get(_ context.Context, service, account string) (string, error) {
	data, err := os.ReadFile(f.path(service, account))
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrSecretNotFound
		}
		return "", err
	}
	return string(data), nil
}

func (f *FileSecretStore) Delete(_ context.Context, service, account string) error {
	err := os.Remove(f.path(service, account))
	if os.IsNotExist(err) {
		return ErrSecretNotFound
	}
	return err
}
