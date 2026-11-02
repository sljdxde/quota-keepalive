#!/usr/bin/env bash
# QKA 一键构建 .app 并打包可安装 DMG（拖拽到 Applications）。
#
# 用法：
#   scripts/build-dmg.sh                # 本机架构（Apple Silicon = arm64）
#   ARCH=universal scripts/build-dmg.sh # 通用二进制（arm64 + amd64）
#
# 产物：
#   dist/dmg/QKA-<version>-<arch>.dmg
set -euo pipefail

# ---- 路径与变量 ----
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

ARCH="${ARCH:-$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')}"
VERSION="${QKA_VERSION:-$(git describe --tags --always 2>/dev/null || echo dev)}"
VERSION="${VERSION#v}"
if [[ ! "$VERSION" =~ ^[0-9A-Za-z._+-]+$ ]]; then
  echo "invalid version: $VERSION" >&2
  exit 2
fi
if [[ ! "$VERSION" =~ ^[0-9] ]]; then
  VERSION="0.0.0-$VERSION"
fi
BUILD="${QKA_BUILD_NUMBER:-$(date +%Y%m%d%H%M%S)}"
if [[ ! "$BUILD" =~ ^[0-9]+$ ]]; then
  echo "QKA_BUILD_NUMBER must contain digits only" >&2
  exit 2
fi
case "$ARCH" in
  arm64|amd64|universal) ;;
  *) echo "unsupported ARCH=$ARCH" >&2; exit 2 ;;
esac
APP_NAME="QKA"
DIST="$ROOT/dist"
WORK="$DIST/app"
APP_BUNDLE="$WORK/$APP_NAME.app"
MACOS_DIR="$APP_BUNDLE/Contents/MacOS"
RES_DIR="$APP_BUNDLE/Contents/Resources"

echo "==> 版本 $VERSION ($BUILD) 架构 $ARCH"

# ---- 1. 生成 App 图标 ----
echo "==> 生成 App 图标"
go run ./packaging/genicon 2>/dev/null
ICONSET="$WORK/AppIcon.iconset"
rm -rf "$ICONSET" "$WORK/AppIcon.icns"
mkdir -p "$ICONSET"
SRC="$ROOT/packaging/appicon_1024.png"
gen() { sips -z "$2" "$2" "$SRC" --out "$ICONSET/$1" >/dev/null 2>&1; }
gen icon_16x16.png 16
gen icon_16x16@2x.png 32
gen icon_32x32.png 32
gen icon_32x32@2x.png 64
gen icon_128x128.png 128
gen icon_128x128@2x.png 256
gen icon_256x256.png 256
gen icon_256x256@2x.png 512
gen icon_512x512.png 512
gen icon_512x512@2x.png 1024
iconutil -c icns "$ICONSET" -o "$WORK/AppIcon.icns"

# ---- 2. 编译 ----
echo "==> 编译 QKA（CGO）"
mkdir -p "$DIST/build"
build_arch() {
  local goarch="$1" out="$2"
  echo "    -> $goarch"
  CGO_ENABLED=1 GOOS=darwin GOARCH="$goarch" \
    go build -trimpath \
      -ldflags "-X qka/internal/cli.Version=$VERSION -X qka/internal/cli.BuildFlavor=gui" \
      -o "$out" ./cmd/qka
}

BIN_OUT="$DIST/build/qka"
if [[ "$ARCH" == "universal" ]]; then
  build_arch arm64 "$DIST/build/qka-arm64"
  build_arch amd64 "$DIST/build/qka-amd64"
  lipo -create -output "$BIN_OUT" "$DIST/build/qka-arm64" "$DIST/build/qka-amd64"
else
  goarch="$ARCH"
  build_arch "$goarch" "$BIN_OUT"
fi

bun_archs() {
  lipo -archs "$1" 2>/dev/null | tr ' ' '\n'
}

resolve_single_bun() {
  local arch="$1"
  local var path
  case "$arch" in
    arm64) var="BUN_ARM64"; path="${BUN_ARM64:-${BUN_PATH:-}}" ;;
    amd64) var="BUN_AMD64"; path="${BUN_AMD64:-${BUN_PATH:-}}" ;;
    *) echo "unsupported Bun architecture: $arch" >&2; exit 2 ;;
  esac
  if [[ -z "$path" ]]; then
    path="$(command -v bun || true)"
  fi
  if [[ -z "$path" || ! -f "$path" ]]; then
    echo "Bun runtime for $arch is required; set $var or BUN_PATH" >&2
    exit 2
  fi
  if ! bun_archs "$path" | grep -qx "$arch"; then
    echo "Bun runtime $path does not contain $arch" >&2
    exit 2
  fi
  printf '%s\n' "$path"
}

# ---- 3. 组装 .app bundle ----
echo "==> 组装 $APP_NAME.app"
rm -rf "$APP_BUNDLE"
mkdir -p "$MACOS_DIR" "$RES_DIR"
if [[ ! -d "$ROOT/provider-plugins" ]]; then
  echo "provider-plugins directory is required" >&2
  exit 2
fi
if [[ ! -d "$ROOT/adapters" ]]; then
  echo "adapters directory is required" >&2
  exit 2
fi
cp "$BIN_OUT" "$MACOS_DIR/qka"
chmod +x "$MACOS_DIR/qka"
cp "$WORK/AppIcon.icns" "$RES_DIR/AppIcon.icns"
cp -R "$ROOT/provider-plugins" "$RES_DIR/provider-plugins"
find "$RES_DIR/provider-plugins" -name ".DS_Store" -delete 2>/dev/null || true

# Info.plist（替换版本占位）
sed -e "s/__VERSION__/$VERSION/g" -e "s/__BUILD__/$BUILD/g" \
  packaging/Info.plist > "$APP_BUNDLE/Contents/Info.plist"

# 适配器与 Bun 运行时放在可执行文件同级（与 cli 的 sibling 解析一致）
cp -R adapters "$MACOS_DIR/adapters"
rm -rf "$MACOS_DIR/adapters/_template"
find "$MACOS_DIR/adapters" -name ".DS_Store" -delete 2>/dev/null || true
if [[ "$ARCH" == "universal" ]]; then
  bun_arm64="$(resolve_single_bun arm64)"
  bun_amd64="$(resolve_single_bun amd64)"
  lipo -create -output "$MACOS_DIR/bun" "$bun_arm64" "$bun_amd64"
else
  cp "$(resolve_single_bun "$ARCH")" "$MACOS_DIR/bun"
fi
chmod +x "$MACOS_DIR/bun"
if [[ "$ARCH" == "universal" ]]; then
  for arch in arm64 amd64; do
    bun_archs "$MACOS_DIR/bun" | grep -qx "$arch" || {
      echo "bundled Bun is missing $arch" >&2
      exit 2
    }
  done
else
  bun_archs "$MACOS_DIR/bun" | grep -qx "$ARCH" || {
    echo "bundled Bun architecture does not match $ARCH" >&2
    exit 2
  }
fi

# PkgInfo
echo -n "APPL????" > "$APP_BUNDLE/Contents/PkgInfo"

# ---- 4. 临时签名（ad-hoc） ----
echo "==> Ad-hoc 代码签名"
codesign --force --deep --sign - \
  --options runtime --timestamp=none \
  "$APP_BUNDLE"
codesign --verify --deep --strict --verbose=2 "$APP_BUNDLE"

# ---- 5. 打包 DMG ----
echo "==> 打包 DMG"
DMG_STAGE="$DIST/dmg-stage"
rm -rf "$DMG_STAGE"
mkdir -p "$DMG_STAGE"
cp -R "$APP_BUNDLE" "$DMG_STAGE/$APP_NAME.app"
ln -s /Applications "$DMG_STAGE/Applications"

DMG_NAME="QKA-$VERSION-$ARCH"
DMG_OUT="$DIST/dmg"
mkdir -p "$DMG_OUT"
DMG_FILE="$DMG_OUT/$DMG_NAME.dmg"
rm -f "$DMG_FILE"

# 先建可读写镜像，设置窗口布局，再转换为压缩只读镜像
hdiutil create -volname "QKA" -srcfolder "$DMG_STAGE" \
  -ov -format UDRW "$DIST/dmg-rw.dmg" >/dev/null

# 挂载并美化（拖拽布局）。只解析并卸载本次创建的卷，绝不按固定名称
# detach 用户已经挂载的其他磁盘。
ATTACH_OUTPUT="$(hdiutil attach "$DIST/dmg-rw.dmg" -nobrowse -readwrite)"
MOUNT_DIR="$(printf '%s\n' "$ATTACH_OUTPUT" | awk -F '\t' 'END {print $NF}')"
if [[ -z "$MOUNT_DIR" || ! -d "$MOUNT_DIR" ]]; then
  echo "failed to resolve QKA DMG mount point" >&2
  exit 1
fi
cleanup_mount() {
  hdiutil detach "$MOUNT_DIR" >/dev/null 2>&1 || true
}
trap cleanup_mount EXIT
DISK_NAME="$(basename "$MOUNT_DIR")"

# 用 AppleScript 设置 Finder 视图与图标位置（失败不阻断，仅影响美观）。
# 注意：首次授权 Finder 自动化时 osascript 会弹权限框并静默挂起，
# 无人值守构建不能被它卡住，因此这里强制最多等 8 秒。
run_with_timeout() {
  local seconds="$1"; shift
  "$@" &
  local cmd_pid=$!
  ( sleep "$seconds"; kill -0 "$cmd_pid" 2>/dev/null && kill "$cmd_pid" 2>/dev/null ) &
  local watchdog_pid=$!
  wait "$cmd_pid" 2>/dev/null || true
  kill "$watchdog_pid" 2>/dev/null || true
  wait "$watchdog_pid" 2>/dev/null || true
}

run_with_timeout 8 osascript 2>/dev/null <<APPLESCRIPT || true
tell application "Finder"
  set diskName to "$DISK_NAME"
  set diskContainer to disk diskName
  set current view of container window of diskContainer to icon view
  set toolbar visible of container window of diskContainer to false
  set statusbar visible of container window of diskContainer to false
  set bounds of container window of diskContainer to {0, 0, 480, 320}
  set viewOptions to icon view options of container window of diskContainer
  set arrangement of viewOptions to not arranged
  set iconSize of viewOptions to 96
  set position of item "QKA" of diskContainer to {120, 130}
  set position of item "Applications" of diskContainer to {360, 130}
end tell
APPLESCRIPT

hdiutil detach "$MOUNT_DIR" >/dev/null
trap - EXIT
hdiutil convert "$DIST/dmg-rw.dmg" -format UDZO -imagekey zlib-level=9 -o "$DMG_FILE" >/dev/null
rm -f "$DIST/dmg-rw.dmg"
rm -rf "$DMG_STAGE"

# 校验 DMG
hdiutil verify "$DMG_FILE" >/dev/null
SHA="$(shasum -a 256 "$DMG_FILE" | awk '{print $1}')"

echo ""
echo "==> 完成"
echo "    DMG: $DMG_FILE"
echo "    SHA256: $SHA"
