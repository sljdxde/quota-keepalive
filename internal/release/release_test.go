package release

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSHA256SUMS(t *testing.T) {
	out := SHA256SUMS([]Artifact{
		{Name: "qka-darwin-arm64", Data: []byte("a")},
		{Name: "qka-darwin-amd64", Data: []byte("b")},
	})
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("应每个制品一行，实际 %d 行", len(lines))
	}
	for _, line := range lines {
		if !strings.Contains(line, "  ") || len(line) < 66 {
			t.Errorf("行格式应为 <64hex>  <name>，实际 %q", line)
		}
	}
}

func TestGenerateFeed(t *testing.T) {
	data, err := GenerateFeed("1.0.0", []Artifact{
		{Name: "darwin-arm64", Data: []byte("a")},
		{Name: "darwin-amd64", Data: []byte("b")},
	})
	if err != nil {
		t.Fatal(err)
	}
	var feed Feed
	if err := json.Unmarshal(data, &feed); err != nil {
		t.Fatal(err)
	}
	if feed.Stable.Version != "1.0.0" || len(feed.Stable.Artifacts) != 2 {
		t.Fatalf("feed.json 内容错误: %s", data)
	}
	if feed.Stable.Artifacts[0].Platform != "darwin/arm64" {
		t.Errorf("平台映射错误，实际 %s", feed.Stable.Artifacts[0].Platform)
	}
	for _, a := range feed.Stable.Artifacts {
		if a.SHA256 == "" {
			t.Error("每个制品应含哈希")
		}
	}
}
