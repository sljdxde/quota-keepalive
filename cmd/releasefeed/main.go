package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"qka/internal/atomicfile"
	"qka/internal/release"
)

const defaultBaseURL = "https://github.com/sljdxde/quota-keepalive/releases/latest/download"

var stableVersionPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+){1,3}$`)

var (
	exitProcess            = os.Exit
	stdout       io.Writer = os.Stdout
	stderr       io.Writer = os.Stderr
	readFile               = os.ReadFile
	generateFeed           = release.GenerateFeed
)

type artifactValues []string

func (values *artifactValues) String() string {
	return strings.Join(*values, ",")
}

func (values *artifactValues) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func main() {
	exitProcess(run(os.Args[1:], stdout, stderr))
}

func run(args []string, out, errOut io.Writer) int {
	if err := execute(args, out); err != nil {
		fmt.Fprintln(errOut, "releasefeed:", err)
		return 2
	}
	return 0
}

func execute(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("releasefeed", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	version := fs.String("version", "", "发布版本，例如 1.2.3")
	output := fs.String("out", filepath.Join("dist", "feed.json"), "feed.json 输出路径")
	baseURL := fs.String("base-url", defaultBaseURL, "制品下载 URL 前缀")
	requirePlatforms := fs.String("require-platforms", "", "必须全部存在的平台，逗号分隔")
	var values artifactValues
	fs.Var(&values, "artifact", "重复传入 platform=path，例如 darwin/arm64=dist/QKA.dmg")
	if err := fs.Parse(args); err != nil {
		return err
	}

	versionValue := strings.TrimSpace(*version)
	if versionValue == "" {
		return errors.New("--version is required")
	}
	if !stableVersionPattern.MatchString(versionValue) {
		return errors.New("--version must be a numeric dotted stable version")
	}
	if len(values) == 0 {
		return errors.New("at least one --artifact is required")
	}
	if strings.TrimSpace(*output) == "" {
		return errors.New("--out is required")
	}
	base := strings.TrimRight(strings.TrimSpace(*baseURL), "/")
	if base == "" {
		return errors.New("--base-url is required")
	}
	required, err := parsePlatforms(*requirePlatforms)
	if err != nil {
		return err
	}

	seen := make(map[string]struct{}, len(values))
	artifacts := make([]release.Artifact, 0, len(values))
	for _, value := range values {
		platform, path, ok := strings.Cut(value, "=")
		platform = strings.TrimSpace(platform)
		path = strings.TrimSpace(path)
		if !ok || platform == "" || path == "" {
			return fmt.Errorf("invalid --artifact %q; want platform=path", value)
		}
		if _, exists := seen[platform]; exists {
			return fmt.Errorf("duplicate platform %q", platform)
		}
		seen[platform] = struct{}{}
		data, err := readFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		name := filepath.Base(path)
		if !strings.Contains(name, versionValue) {
			return fmt.Errorf("artifact %s does not contain version %s", name, versionValue)
		}
		artifacts = append(artifacts, release.Artifact{
			Platform: platform,
			Name:     name,
			Data:     data,
			URL:      base + "/" + url.PathEscape(name),
		})
	}
	var missing []string
	for platform := range required {
		if _, ok := seen[platform]; !ok {
			missing = append(missing, platform)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("missing required platforms: %s", strings.Join(missing, ","))
	}

	data, err := generateFeed(versionValue, artifacts)
	if err != nil {
		return err
	}
	dir := filepath.Dir(*output)
	if dir != "." {
		if err := atomicfile.EnsureDir(dir, 0o755); err != nil {
			return err
		}
	}
	if err := atomicfile.WriteFile(*output, data, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "feed.json: %s (%d artifacts)\n", *output, len(artifacts))
	return nil
}

func parsePlatforms(raw string) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out, nil
	}
	for _, item := range strings.Split(raw, ",") {
		platform := strings.TrimSpace(item)
		if platform == "" {
			return nil, errors.New("--require-platforms contains an empty platform")
		}
		out[platform] = struct{}{}
	}
	return out, nil
}
