package pairing

import (
	"os"
	"path/filepath"

	"qka/internal/atomicfile"
)

// pairingFile 是配对凭据哈希的持久化文件名。
const pairingFile = "pairing.json"

// Save 将配对凭据哈希写入 <configDir>/pairing.json（0600）。
func Save(configDir string, p *Pairing) error {
	data, _ := p.MarshalJSON()
	if err := atomicfile.EnsureDir(configDir, 0o700); err != nil {
		return err
	}
	return atomicfile.WriteFile(filepath.Join(configDir, pairingFile), data, 0o600)
}

// LoadPairing 从 pairing.json 读取；文件不存在时返回空配对。
func LoadPairing(configDir string) (*Pairing, error) {
	data, err := os.ReadFile(filepath.Join(configDir, pairingFile))
	if err != nil {
		if os.IsNotExist(err) {
			return New(), nil
		}
		return nil, err
	}
	return Load(data)
}
