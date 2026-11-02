// Package release 生成发布归档所需的 SHA256SUMS 与 feed.json。
package release

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// Artifact 是归档中的一个制品。
type Artifact struct {
	Platform string
	Name     string
	Data     []byte
	URL      string
}

// SHA256SUMS 生成「<hex>  <name>」逐行的校验清单。
func SHA256SUMS(artifacts []Artifact) []byte {
	var b strings.Builder
	for _, a := range artifacts {
		sum := sha256.Sum256(a.Data)
		b.WriteString(hex.EncodeToString(sum[:]))
		b.WriteString("  ")
		b.WriteString(a.Name)
		b.WriteString("\n")
	}
	return []byte(b.String())
}

// FeedArtifact 是 feed.json 中的一个平台制品。
type FeedArtifact struct {
	Platform string `json:"platform"`
	Name     string `json:"name"`
	SHA256   string `json:"sha256"`
	URL      string `json:"url,omitempty"`
}

// Feed 是 feed.json 结构。
type Feed struct {
	Stable FeedStable `json:"stable"`
}

// FeedStable 是稳定版。
type FeedStable struct {
	Version   string         `json:"version"`
	Artifacts []FeedArtifact `json:"artifacts"`
}

// GenerateFeed 生成 feed.json。
func GenerateFeed(version string, artifacts []Artifact) ([]byte, error) {
	var entries []FeedArtifact
	for _, a := range artifacts {
		sum := sha256.Sum256(a.Data)
		platform := strings.TrimSpace(a.Platform)
		if platform == "" {
			platform = inferPlatform(a.Name)
		}
		entries = append(entries, FeedArtifact{
			Platform: platform, Name: a.Name, SHA256: hex.EncodeToString(sum[:]), URL: a.URL,
		})
	}
	data, _ := json.MarshalIndent(Feed{Stable: FeedStable{
		Version: version, Artifacts: entries,
	}}, "", "  ")
	return append(data, '\n'), nil
}

func inferPlatform(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "darwin-arm64"):
		return "darwin/arm64"
	case strings.Contains(lower, "darwin-amd64"):
		return "darwin/amd64"
	case strings.Contains(lower, "windows-arm64"):
		return "windows/arm64"
	case strings.Contains(lower, "windows-amd64"):
		return "windows/amd64"
	case strings.HasSuffix(lower, "-arm64.dmg"):
		return "darwin/arm64"
	case strings.HasSuffix(lower, "-amd64.dmg"):
		return "darwin/amd64"
	default:
		return name
	}
}
