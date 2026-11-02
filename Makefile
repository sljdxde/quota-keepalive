# QKA 发布构建矩阵。
# - macOS 双架构（darwin/arm64、darwin/amd64）GUI 原生构建，需要 CGO。
# - Windows 双架构 GUI/CLI 交叉构建并生成便携包；真实 Windows 行为需另机验收。

VERSION ?= $(shell git describe --tags --always 2>/dev/null | sed 's/^v//' || echo dev)
WINVER  := $(shell echo $(VERSION) | sed -E 's/^v//; s/[^0-9.].*//')
WIN_ARCH ?= all
DIST    ?= dist
PKG     := ./cmd/qka
BUN     := $(shell which bun)
LDFLAGS := -X qka/internal/cli.Version=$(VERSION)
GUI_LDFLAGS := $(LDFLAGS) -X qka/internal/cli.BuildFlavor=gui

.PHONY: build-darwin-arm64 build-darwin-amd64 build-linux build-windows \
	build-windows-cli release-windows release-macos-arm64 release-macos-amd64 \
	release-macos release-feed release-all release test vet fmt

build-darwin-arm64:
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -trimpath \
		-ldflags "$(LDFLAGS)" -o $(DIST)/build/qka-darwin-arm64 $(PKG)

build-darwin-amd64:
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 go build -trimpath \
		-ldflags "$(LDFLAGS)" -o $(DIST)/build/qka-darwin-amd64 $(PKG)

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
		-ldflags "$(LDFLAGS)" -o $(DIST)/build/qka-linux-amd64 $(PKG)

build-windows:
	@mkdir -p $(DIST)/build
	@go run github.com/tc-hib/go-winres@v0.3.3 make \
		--in build/windows/winres.json --arch amd64,arm64 --out rsrc \
		--file-version "$(or $(WINVER),0.0.0)" \
		--product-version "$(or $(WINVER),0.0.0)"
	@for arch in amd64 arm64; do \
		echo "  windows/$$arch GUI"; \
		CGO_ENABLED=0 GOOS=windows GOARCH=$$arch go build -trimpath \
			-ldflags "$(GUI_LDFLAGS) -s -w -H windowsgui" \
			-o $(DIST)/build/QKA-windows-$$arch.exe $(PKG) || exit 1; \
	done
	@rm -f rsrc_windows_*.syso

# Windowless GUI builds cannot print to a terminal. Keep a console binary for
# serve/task-run/doctor and other CLI-only workflows.
build-windows-cli:
	@mkdir -p $(DIST)/build
	@for arch in amd64 arm64; do \
		echo "  windows/$$arch CLI"; \
		CGO_ENABLED=0 GOOS=windows GOARCH=$$arch go build -trimpath \
			-ldflags "$(LDFLAGS) -s -w" \
			-o $(DIST)/build/QKA-cli-windows-$$arch.exe $(PKG) || exit 1; \
	done

# Windows portable release: per-arch ZIP containing GUI, console CLI, adapters,
# Bun and a short README. This cross-builds and packages; real Windows Task
# Scheduler acceptance still requires a Windows machine.
release-windows: build-windows build-windows-cli
	@VERSION="$(VERSION)" DIST="$(DIST)" ARCH="$(WIN_ARCH)" \
		scripts/package-windows.sh

# 汇总已生成的 macOS DMG 与 Windows ZIP，生成官方 feed.json 和 SHA256SUMS。
release-feed:
	@VERSION="$(VERSION)" DIST="$(abspath $(DIST))" \
		scripts/build-release-feed.sh

# macOS 发布使用可安装 DMG，不再把裸二进制当作更新制品。
release-macos-arm64:
	@QKA_VERSION="$(VERSION)" ARCH=arm64 scripts/build-dmg.sh

release-macos-amd64:
	@QKA_VERSION="$(VERSION)" ARCH=amd64 scripts/build-dmg.sh

release-macos: release-macos-arm64 release-macos-amd64

# 完整发布必须同时具备 macOS 双架构与 Windows 双架构制品。
release-all: release-macos release-windows release-feed

release: release-macos

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .
