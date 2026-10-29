// Package pairing 管理 MCP 端点的 per-宿主配对凭据：本地只存哈希，支持独立吊销与旋转。
package pairing

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
)

// Pairing 持有各宿主凭据的哈希。
type Pairing struct {
	hosts map[string]hostRecord
	rand  io.Reader
}

type hostRecord struct {
	Hash string `json:"hash"`
}

// New 创建空配对集合。
func New() *Pairing {
	return &Pairing{hosts: map[string]hostRecord{}, rand: rand.Reader}
}

// withRand 覆盖随机源（测试用确定性源）。
func (p *Pairing) withRand(r io.Reader) *Pairing {
	p.rand = r
	return p
}

func newToken(r io.Reader) (string, error) {
	b := make([]byte, 32)
	if _, err := io.ReadFull(r, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// Issue 为宿主生成新凭据并只保存其哈希，返回明文（仅返回一次）。
func (p *Pairing) Issue(hostID string) (string, error) {
	plain, err := newToken(p.rand)
	if err != nil {
		return "", err
	}
	p.hosts[hostID] = hostRecord{Hash: hashToken(plain)}
	return plain, nil
}

// Verify 以恒定时间比对宿主凭据。
func (p *Pairing) Verify(hostID, plain string) bool {
	rec, ok := p.hosts[hostID]
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(rec.Hash), []byte(hashToken(plain))) == 1
}

// Revoke 吊销单个宿主凭据。
func (p *Pairing) Revoke(hostID string) {
	delete(p.hosts, hostID)
}

// Rotate 旋转单个宿主凭据：旧凭据失效，返回新凭据；不影响其他宿主。
func (p *Pairing) Rotate(hostID string) (string, error) {
	delete(p.hosts, hostID)
	return p.Issue(hostID)
}

// MarshalJSON 序列化凭据哈希（供 0600 持久化）。
func (p *Pairing) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.hosts)
}

// Load 从 JSON 恢复凭据哈希。
func Load(data []byte) (*Pairing, error) {
	p := New()
	if err := json.Unmarshal(data, &p.hosts); err != nil {
		return nil, err
	}
	return p, nil
}
