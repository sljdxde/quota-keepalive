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
	Name string
	Data []byte
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
	platforms := map[string]string{
		"darwin-arm64": "darwin/arm64",
		"darwin-amd64": "darwin/amd64",
	}
	var entries []FeedArtifact
	for _, a := range artifacts {
		sum := sha256.Sum256(a.Data)
		platform := a.Name
		if p, ok := platforms[a.Name]; ok {
			platform = p
		}
		entries = append(entries, FeedArtifact{
			Platform: platform, Name: a.Name, SHA256: hex.EncodeToString(sum[:]),
		})
	}
	data, err := json.MarshalIndent(Feed{Stable: FeedStable{
		Version: version, Artifacts: entries,
	}}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
