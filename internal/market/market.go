// Package market 实现签名适配器市场：仅加载经 ed25519 签名与完整制品哈希
// 验证的适配器，支持版本固定与失败回退，绝不以未知版本访问凭据。
package market

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
)

var (
	// ErrHashMismatch 表示制品哈希不匹配。
	ErrHashMismatch = errors.New("market: 制品哈希不匹配")
	// ErrErrBadSignature 表示 ed25519 签名验证失败。
	ErrBadSignature = errors.New("market: 签名验证失败")
)

// Release 是一个待验证的市场发布。
type Release struct {
	// ID 是适配器 id。
	ID string
	// Version 是发布版本。
	Version string
	// Artifact 是完整制品（含 manifest；效应声明是签名内容）。
	Artifact []byte
	// SHA256 是市场索引中声明的完整制品哈希。
	SHA256 []byte
	// Signature 是对 Artifact 的 ed25519 签名。
	Signature []byte
}

// VerifyRelease 验证完整制品哈希与 ed25519 签名；任一失败即拒绝。
func VerifyRelease(publicKey ed25519.PublicKey, r Release) error {
	sum := sha256.Sum256(r.Artifact)
	if !bytes.Equal(sum[:], r.SHA256) {
		return ErrHashMismatch
	}
	if !ed25519.Verify(publicKey, r.Artifact, r.Signature) {
		return ErrBadSignature
	}
	return nil
}
