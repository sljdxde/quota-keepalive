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
		{Name: "QKA-1.0.0-windows-arm64.zip", Platform: "windows/arm64", Data: []byte("b")},
		{Name: "QKA-1.0.0-amd64.dmg", Data: []byte("c")},
		{Name: "QKA-1.0.0-windows-amd64.zip", Data: []byte("d")},
		{Name: "unknown.bin", Data: []byte("e")},
		{Name: "QKA-1.0.0-windows-arm64.zip", Data: []byte("f")},
		{Name: "darwin-amd64", Data: []byte("g")},
		{Name: "QKA-1.0.0-arm64.dmg", Data: []byte("h")},
	})
	if err != nil {
		t.Fatal(err)
	}
	var feed Feed
	if err := json.Unmarshal(data, &feed); err != nil {
		t.Fatal(err)
	}
	if feed.Stable.Version != "1.0.0" || len(feed.Stable.Artifacts) != 8 {
		t.Fatalf("feed.json 内容错误: %s", data)
	}
	want := []string{
		"darwin/arm64", "windows/arm64", "darwin/amd64",
		"windows/amd64", "unknown.bin", "windows/arm64",
		"darwin/amd64", "darwin/arm64",
	}
	for i, platform := range want {
		if feed.Stable.Artifacts[i].Platform != platform {
			t.Errorf("artifact %d 平台映射错误，实际 %s", i, feed.Stable.Artifacts[i].Platform)
		}
	}
	for _, a := range feed.Stable.Artifacts {
		if a.SHA256 == "" {
			t.Error("每个制品应含哈希")
		}
	}
}
