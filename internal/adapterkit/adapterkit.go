// Package adapterkit 提供适配器脚手架（Init）与离线校验（Validate）：
// 不联网、不读凭据，校验 manifest schema、动作/效应完备性、入口存在与兼容范围。
package adapterkit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"qka/internal/adapterhost"
)

// ManifestName 是适配器清单文件名。
const ManifestName = "manifest.json"

// Validate 离线校验一个适配器目录。
func Validate(dir string) error {
	data, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return fmt.Errorf("无法读取 %s: %w", ManifestName, err)
	}
	var m adapterhost.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("%s 解析失败: %w", ManifestName, err)
	}
	if err := m.Validate(); err != nil {
		return err
	}
	if len(m.Actions) == 0 {
		return errors.New("manifest 未声明任何动作")
	}
	for _, a := range m.Actions {
		if !effectsDeclared(a.Effects) {
			return fmt.Errorf("动作 %s 未声明效应", a.Op)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, m.Entry)); err != nil {
		return fmt.Errorf("入口文件 %s 不存在: %w", m.Entry, err)
	}
	if m.Compatibility.QKA != "" && !validRange(m.Compatibility.QKA) {
		return fmt.Errorf("坏兼容范围: %s", m.Compatibility.QKA)
	}
	return nil
}

func effectsDeclared(e adapterhost.Effects) bool {
	return e.AccessesNetwork != nil || e.MutatesTarget != nil || e.SpendsCredits != nil ||
		e.Idempotent != nil || e.MayRequireReauth != nil || e.MutatesLocalState != nil
}

func validRange(r string) bool {
	r = strings.TrimSpace(r)
	for _, p := range []string{">=", ">", "^", "~", "="} {
		if strings.HasPrefix(r, p) {
			return true
		}
	}
	return len(r) > 0 && r[0] >= '0' && r[0] <= '9'
}

// Init 在 dir 生成可运行的适配器骨架（含 manifest、入口、package 与说明）。
func Init(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	files := map[string]string{
		ManifestName:   skeletonManifest,
		"index.mjs":    skeletonIndex,
		"package.json": skeletonPackage,
		"README.md":    skeletonReadme,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}
