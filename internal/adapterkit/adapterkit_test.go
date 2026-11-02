package adapterkit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitGeneratesValidSkeleton(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my-adapter")
	if err := Init(dir); err != nil {
		t.Fatalf("Init 失败: %v", err)
	}
	if err := Validate(dir); err != nil {
		t.Fatalf("生成的骨架应通过校验，实际: %v", err)
	}
}

func TestValidateMalformedManifestAndSchemaFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Validate(dir); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte(`{"schema":"bad"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Validate(dir); err == nil {
		t.Fatal("无效 manifest schema 应报错")
	}
}

func TestInitMkdirAndWriteFailures(t *testing.T) {
	parent := t.TempDir()
	file := filepath.Join(parent, "not-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Init(filepath.Join(file, "child")); err == nil {
		t.Fatal("父路径是文件时 Init 应失败")
	}

	dir := filepath.Join(parent, "adapter")
	if err := os.MkdirAll(filepath.Join(dir, ManifestName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Init(dir); err == nil {
		t.Fatal("目标清单是目录时写入应失败")
	}
}

func TestValidateMissingManifest(t *testing.T) {
	dir := t.TempDir()
	if err := Validate(dir); err == nil {
		t.Fatal("缺 manifest 应报错")
	}
}

func TestValidateMissingAction(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "manifest.json", `{
		"id":"x","name":"X","version":"0.1","entry":"index.mjs","actions":[]
	}`)
	writeFile(t, dir, "index.mjs", "// x")
	if err := Validate(dir); err == nil {
		t.Fatal("未声明动作应报错")
	}
}

func TestValidateMissingEffects(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "manifest.json", `{
		"id":"x","name":"X","version":"0.1","entry":"index.mjs",
		"actions":[{"op":"accounts.discover"}]
	}`)
	writeFile(t, dir, "index.mjs", "// x")
	if err := Validate(dir); err == nil {
		t.Fatal("动作缺效应应报错")
	}
}

func TestValidateMissingEntryFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "manifest.json", `{
		"id":"x","name":"X","version":"0.1","entry":"index.mjs",
		"actions":[{"op":"accounts.discover","effects":{"accessesNetwork":true}}]
	}`)
	if err := Validate(dir); err == nil {
		t.Fatal("缺入口文件应报错")
	}
}

func TestValidateBadCompatibility(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "manifest.json", `{
		"id":"x","name":"X","version":"0.1","entry":"index.mjs",
		"actions":[{"op":"accounts.discover","effects":{"accessesNetwork":true}}],
		"compatibility":{"qka":"not-a-range"}
	}`)
	writeFile(t, dir, "index.mjs", "// x")
	if err := Validate(dir); err == nil {
		t.Fatal("坏兼容范围应报错")
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
