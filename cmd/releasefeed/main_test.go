package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"qka/internal/release"
)

func TestRunAndExecuteSuccess(t *testing.T) {
	dir := t.TempDir()
	mac := filepath.Join(dir, "QKA-1.2.3-arm64.dmg")
	windows := filepath.Join(dir, "QKA-1.2.3-windows-amd64.zip")
	if err := os.WriteFile(mac, []byte("mac"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(windows, []byte("windows"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "release", "feed.json")
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--version", "1.2.3",
		"--out", out,
		"--base-url", "https://downloads.example/releases/",
		"--require-platforms", "darwin/arm64,windows/amd64",
		"--artifact", "darwin/arm64=" + mac,
		"--artifact", "windows/amd64=" + windows,
	}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("run code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "2 artifacts") {
		t.Fatalf("stdout=%q", stdout.String())
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("darwin/arm64")) ||
		!bytes.Contains(data, []byte("https://downloads.example/releases/QKA-1.2.3-arm64.dmg")) {
		t.Fatalf("feed=%s", data)
	}
}

func TestExecuteValidationFailures(t *testing.T) {
	dir := t.TempDir()
	valid := filepath.Join(dir, "QKA-1.0.0-artifact.zip")
	if err := os.WriteFile(valid, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	mismatch := filepath.Join(dir, "artifact.zip")
	if err := os.WriteFile(mismatch, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "flag parse", args: []string{"--unknown"}, want: "flag provided but not defined"},
		{name: "version", args: []string{}, want: "--version is required"},
		{name: "invalid version", args: []string{"--version", "v1.0.0"}, want: "numeric dotted stable version"},
		{name: "artifact required", args: []string{"--version", "1.0.0"}, want: "at least one --artifact"},
		{name: "empty output", args: []string{"--version", "1.0.0", "--out", " ", "--artifact", "darwin/arm64=" + valid}, want: "--out is required"},
		{name: "empty base", args: []string{"--version", "1.0.0", "--base-url", " ", "--artifact", "darwin/arm64=" + valid}, want: "--base-url is required"},
		{name: "empty required platform", args: []string{"--version", "1.0.0", "--require-platforms", "darwin/arm64,", "--artifact", "darwin/arm64=" + valid}, want: "empty platform"},
		{name: "malformed", args: []string{"--version", "1.0.0", "--artifact", "darwin/arm64"}, want: "invalid --artifact"},
		{name: "duplicate", args: []string{"--version", "1.0.0", "--artifact", "darwin/arm64=" + valid, "--artifact", "darwin/arm64=" + valid}, want: "duplicate platform"},
		{name: "version mismatch", args: []string{"--version", "1.0.0", "--artifact", "darwin/arm64=" + mismatch}, want: "does not contain version"},
		{name: "missing required platform", args: []string{"--version", "1.0.0", "--require-platforms", "windows/amd64", "--artifact", "darwin/arm64=" + valid}, want: "missing required platforms"},
		{name: "missing file", args: []string{"--version", "1.0.0", "--artifact", "darwin/arm64=" + filepath.Join(dir, "missing")}, want: "read "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := execute(tt.args, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("execute error=%v want=%q", err, tt.want)
			}
		})
	}
}

func TestExecuteGenerateAndWriteFailures(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "QKA-1.0.0-artifact.zip")
	if err := os.WriteFile(artifact, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldGenerate := generateFeed
	t.Cleanup(func() { generateFeed = oldGenerate })
	generateFeed = func(string, []release.Artifact) ([]byte, error) {
		return nil, errors.New("generate failed")
	}
	if err := execute([]string{
		"--version", "1.0.0", "--out", filepath.Join(dir, "feed.json"),
		"--artifact", "darwin/arm64=" + artifact,
	}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "generate failed") {
		t.Fatalf("generate error=%v", err)
	}

	generateFeed = oldGenerate
	parentFile := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(parentFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := execute([]string{
		"--version", "1.0.0", "--out", filepath.Join(parentFile, "feed.json"),
		"--artifact", "darwin/arm64=" + artifact,
	}, &bytes.Buffer{}); err == nil {
		t.Fatal("EnsureDir failure must be returned")
	}

	outDir := filepath.Join(dir, "out")
	if err := os.Mkdir(outDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := execute([]string{
		"--version", "1.0.0", "--out", outDir,
		"--artifact", "darwin/arm64=" + artifact,
	}, &bytes.Buffer{}); err == nil {
		t.Fatal("WriteFile failure must be returned")
	}
}

func TestRunFailureAndMain(t *testing.T) {
	var errorOutput bytes.Buffer
	if code := run([]string{"--version", "1.0.0"}, &bytes.Buffer{}, &errorOutput); code != 2 ||
		!strings.Contains(errorOutput.String(), "at least one --artifact") {
		t.Fatalf("code=%d stderr=%q", code, errorOutput.String())
	}

	oldArgs, oldExit, oldStdout, oldStderr := os.Args, exitProcess, stdout, stderr
	t.Cleanup(func() {
		os.Args, exitProcess, stdout, stderr = oldArgs, oldExit, oldStdout, oldStderr
	})
	var exitCode int
	exitProcess = func(code int) { exitCode = code }
	stdout = &bytes.Buffer{}
	stderr = &bytes.Buffer{}
	os.Args = []string{"releasefeed", "--version", "1.0.0"}
	main()
	if exitCode != 2 {
		t.Fatalf("main exit code=%d", exitCode)
	}
}
