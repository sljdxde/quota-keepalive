# qka 发布构建矩阵。
# - macOS 双架构（darwin/arm64、darwin/amd64）GUI 原生构建，需要 CGO。
# - 无 GUI 形态保留 linux/windows 交叉编译路径（CGO_ENABLED=0），不承诺其行为。

VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
DIST    ?= dist
PKG     := ./cmd/qka
BUN     := $(shell which bun)

.PHONY: build-darwin-arm64 build-darwin-amd64 build-linux build-windows release test vet fmt

build-darwin-arm64:
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -o $(DIST)/build/qka-darwin-arm64 $(PKG)

build-darwin-amd64:
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 go build -o $(DIST)/build/qka-darwin-amd64 $(PKG)

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(DIST)/build/qka-linux-amd64 $(PKG)

build-windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o $(DIST)/build/qka-windows-amd64.exe $(PKG)

# 归档：每架构目录含 qka 二进制、Bun 运行时与图标；生成 SHA256SUMS。
release: build-darwin-arm64 build-darwin-amd64
	@for arch in arm64 amd64; do \
		mkdir -p $(DIST)/qka-darwin-$$arch; \
		cp $(DIST)/build/qka-darwin-$$arch $(DIST)/qka-darwin-$$arch/qka; \
		cp $(BUN) $(DIST)/qka-darwin-$$arch/bun 2>/dev/null || true; \
		cp internal/gui/icon.go $(DIST)/qka-darwin-$$arch/ 2>/dev/null || true; \
	done
	cd $(DIST) && shasum -a 256 qka-darwin-arm64/qka qka-darwin-amd64/qka > SHA256SUMS
	@echo "release artifacts in $(DIST); feed.json 由 internal/release.GenerateFeed 生成"

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .
