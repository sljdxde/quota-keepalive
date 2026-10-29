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
VERSION="$(git describe --tags --always 2>/dev/null || echo dev)"
BUILD="$(git rev-parse --short HEAD 2>/dev/null || echo 0)"
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
      -ldflags "-X qka/internal/cli.Version=$VERSION" \
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

# ---- 3. 组装 .app bundle ----
echo "==> 组装 $APP_NAME.app"
rm -rf "$APP_BUNDLE"
mkdir -p "$MACOS_DIR" "$RES_DIR"
cp "$BIN_OUT" "$MACOS_DIR/qka"
chmod +x "$MACOS_DIR/qka"
cp "$WORK/AppIcon.icns" "$RES_DIR/AppIcon.icns"

# Info.plist（替换版本占位）
sed -e "s/__VERSION__/$VERSION/g" -e "s/__BUILD__/$BUILD/g" \
  packaging/Info.plist > "$APP_BUNDLE/Contents/Info.plist"

# 适配器与 Bun 运行时放在可执行文件同级（与 cli 的 sibling 解析一致）
cp -R adapters "$MACOS_DIR/adapters"
rm -rf "$MACOS_DIR/adapters/_template"
find "$MACOS_DIR/adapters" -name ".DS_Store" -delete 2>/dev/null || true
if command -v bun >/dev/null 2>&1; then
  cp "$(command -v bun)" "$MACOS_DIR/bun"
  chmod +x "$MACOS_DIR/bun"
fi

# PkgInfo
echo -n "APPL????" > "$APP_BUNDLE/Contents/PkgInfo"

# ---- 4. 临时签名（ad-hoc） ----
echo "==> Ad-hoc 代码签名"
codesign --force --deep --sign - \
  --options runtime --timestamp=none \
  "$APP_BUNDLE" 2>&1 | grep -v "code object is not signed" || true
codesign --verify --deep --strict --verbose=2 "$APP_BUNDLE" 2>&1 | tail -2 || true

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

# 挂载并美化（拖拽布局）
MOUNT_DIR="/Volumes/QKA"
hdiutil detach "$MOUNT_DIR" >/dev/null 2>&1 || true
hdiutil attach "$DIST/dmg-rw.dmg" -nobrowse -readwrite >/dev/null

# 用 AppleScript 设置 Finder 视图与图标位置（失败不阻断，仅影响美观）
osascript <<'APPLESCRIPT' 2>/dev/null || true
tell application "Finder"
  set diskName to "QKA"
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

hdiutil detach "$MOUNT_DIR" >/dev/null 2>&1 || true
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
