// Package selfupdate 实现本体自更新：从固定官方 feed 读取 stable 更新并校验
// SHA-256（镜像只能替换下载前缀），忙时等待 idle，健康检查通过后交接，
// 失败回滚 qka.old，容器内禁用，提供 dry-run。
package selfupdate

import (
	"encoding/json"
	"errors"
	"strings"
)

var (
	// ErrNoUpdate 表示已是最新。
	ErrNoUpdate = errors.New("selfupdate: 已是最新版本")
	// ErrContainerDisabled 表示容器内禁用自更新。
	ErrContainerDisabled = errors.New("selfupdate: 容器内禁用自更新")
	// ErrBadMirror 表示镜像试图替换版本/哈希（只允许替换下载前缀）。
	ErrBadMirror = errors.New("selfupdate: 镜像只能替换下载前缀，不能改版本与哈希")
)

// Feed 是固定官方 feed 结构。
type Feed struct {
	// Stable 是当前稳定版。
	Stable ReleaseInfo `json:"stable"`
}

// ReleaseInfo 是一个发布。
type ReleaseInfo struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
}

// Plan 是 dry-run 计划。
type Plan struct {
	FromVersion string `json:"from_version"`
	ToVersion   string `json:"to_version"`
	URL         string `json:"url"`
	OldPath     string `json:"old_path"`
	Verify      string `json:"verify"`
	Undo        string `json:"undo"`
}

// CheckForUpdate 解析官方 feed，比较版本；mirror 非空时仅替换下载前缀。
func CheckForUpdate(currentVersion string, feedData []byte, mirror string) (*ReleaseInfo, error) {
	var feed Feed
	if err := json.Unmarshal(feedData, &feed); err != nil {
		return nil, err
	}
	r := feed.Stable

	if !newer(r.Version, currentVersion) {
		return nil, ErrNoUpdate
	}

	if mirror != "" {
		url, err := applyMirror(r.URL, mirror)
		if err != nil {
			return nil, err
		}
		r.URL = url
	}
	return &r, nil
}

// DryRun 生成只读计划，不执行更新。
func DryRun(currentVersion string, r ReleaseInfo) Plan {
	return Plan{
		FromVersion: currentVersion,
		ToVersion:   r.Version,
		URL:         r.URL,
		OldPath:     "qka.old",
		Verify:      "下载校验 SHA-256 后等待 idle，健康检查通过才交接",
		Undo:        "首启/健康失败自动回滚 qka.old",
	}
}

func applyMirror(url, mirror string) (string, error) {
	// 只允许替换 scheme://host 前缀，路径必须保留（路径含版本制品）。
	afterScheme, ok := strings.CutPrefix(url, "https://")
	if !ok {
		afterScheme, ok = strings.CutPrefix(url, "http://")
	}
	if !ok {
		return "", ErrBadMirror
	}
	slash := strings.IndexByte(afterScheme, '/')
	if slash < 0 {
		return "", ErrBadMirror
	}
	path := afterScheme[slash:]

	mirror = strings.TrimRight(mirror, "/")
	if !strings.HasPrefix(mirror, "http://") && !strings.HasPrefix(mirror, "https://") {
		return "", ErrBadMirror
	}
	// 镜像若自带路径则视为试图改变制品位置，拒绝。
	mirrorRest, ok := strings.CutPrefix(mirror, "https://")
	if !ok {
		mirrorRest, _ = strings.CutPrefix(mirror, "http://")
	}
	if strings.Contains(mirrorRest, "/") {
		return "", ErrBadMirror
	}
	return mirror + path, nil
}

// newer 报告 target 是否比 current 新（简单点分版本比较）。
func newer(target, current string) bool {
	return compareVersions(target, current) > 0
}
