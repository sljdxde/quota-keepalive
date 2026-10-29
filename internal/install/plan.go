package install

import (
	"bytes"
	"crypto/sha256"
	"errors"
)

var (
	// ErrUnsupportedHost 表示宿主不支持 MCP，无法自动安装。
	ErrUnsupportedHost = errors.New("install: 该宿主不支持 MCP，只能手动接入，不称自动已启用")
	// ErrDownloadChecksum 表示下载物 SHA-256 校验失败。
	ErrDownloadChecksum = errors.New("install: 下载物 SHA-256 校验失败")
)

// Plan 是安装前的只读预演计划。
type Plan struct {
	HostID           string
	DownloadURL      string
	InstallPath      string
	HostConfigChange string
	BackupPath       string
	Verify           string
	Undo             string
	Steps            []string
}

// Preview 生成只读预演；不支持 MCP 的宿主如实报错。
func Preview(hostID string) (Plan, error) {
	profile, ok := ProfileFor(hostID)
	if !ok {
		return Plan{}, ErrUnsupportedHost
	}
	if !profile.MCPSupported {
		return Plan{}, ErrUnsupportedHost
	}
	return Plan{
		HostID:           hostID,
		DownloadURL:      "https://official.example/qka/qka-darwin-arm64",
		InstallPath:      "/Applications/qka.app",
		HostConfigChange: profile.ConfigLocation + "（" + profile.Transport + "）",
		BackupPath:       profile.ConfigLocation + ".bak",
		Verify:           "首启后验证 loopback 连通并完成配对",
		Undo:             profile.Uninstall,
		Steps: []string{
			"下载固定官方源并校验 SHA-256",
			"清除 quarantine 属性",
			"首启 qka",
			"为该宿主写入专属配对凭据（写入前备份）",
			"验证 loopback 连通",
			"引导钥匙串授权并当场验证",
			"引导创建宿主原生计划，仅做观测核验",
		},
	}, nil
}

// VerifyDownload 校验下载物与官方 SHA-256 是否一致。
func VerifyDownload(data, expectedSHA256 []byte) error {
	sum := sha256.Sum256(data)
	if !bytes.Equal(sum[:], expectedSHA256) {
		return ErrDownloadChecksum
	}
	return nil
}
