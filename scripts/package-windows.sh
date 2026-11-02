#!/usr/bin/env bash
# Build portable QKA Windows packages from the Go binaries produced by
# make build-windows build-windows-cli. The package mirrors Magpie's per-arch
# release shape while keeping QKA's adapters and Bun runtime discoverable beside
# QKA.exe.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

VERSION="${VERSION:-$(git describe --tags --always 2>/dev/null || echo dev)}"
VERSION="${VERSION#v}"
VERSION="$(printf '%s' "$VERSION" | tr -cd 'A-Za-z0-9._+-')"
ARCH="${ARCH:-all}"
DIST="${DIST:-$ROOT/dist}"
BUILD="$DIST/build"
BUN_VERSION="${BUN_VERSION:-1.4.4}"
BUN_CACHE="$DIST/cache/bun-v$BUN_VERSION"
SUMMARY="$DIST/SHA256SUMS-windows-$VERSION.txt"

mkdir -p "$DIST" "$BUILD" "$BUN_CACHE"

if ! command -v zip >/dev/null 2>&1; then
  echo "zip is required to package Windows releases" >&2
  exit 2
fi
if [[ ! -d "$ROOT/provider-plugins" ]]; then
  echo "provider-plugins directory is required" >&2
  exit 2
fi
if [[ ! -d "$ROOT/adapters" ]]; then
  echo "adapters directory is required" >&2
  exit 2
fi

archs=()
case "$ARCH" in
  all) archs=(amd64 arm64) ;;
  amd64|arm64) archs=("$ARCH") ;;
  *) echo "unsupported ARCH=$ARCH (want amd64, arm64, or all)" >&2; exit 2 ;;
esac

package_for() {
  case "$1" in
    amd64) echo "@oven/bun-windows-x64" ;;
    arm64) echo "@oven/bun-windows-aarch64" ;;
  esac
}

integrity_for() {
  case "$1" in
    amd64) echo "sha512-a3LNctBVO8PjePsFZfwDleF2JXYf2BfQ1u/PLRnBDLnC2Riml+4+LgJpKj4xRGhB92KweGyQI5PXaTkP9qypMQ==" ;;
    arm64) echo "sha512-n0ep5ZRPCZY1+dqFJpnnRUZB1wy4GYU6At8RF+pyzySCLjkN9R4rFh2PtBHAgF/lg1vOl3/CeER3pDM+p40ATA==" ;;
  esac
}

override_for() {
  case "$1" in
    amd64) printf '%s' "${BUN_WINDOWS_AMD64:-}" ;;
    arm64) printf '%s' "${BUN_WINDOWS_ARM64:-}" ;;
  esac
}

prepare_bun() {
  local arch="$1" out="$2"
  local override
  override="$(override_for "$arch")"
  if [[ -n "$override" ]]; then
    if [[ ! -f "$override" ]]; then
      echo "Bun override does not exist: $override" >&2
      exit 2
    fi
    cp "$override" "$out"
    return
  fi

  local package expected actual tarball
  package="$(package_for "$arch")"
  expected="$(integrity_for "$arch")"
  tarball="$BUN_CACHE/oven-bun-windows-$arch-$BUN_VERSION.tgz"
  if [[ ! -f "$BUN_CACHE/$arch/bun.exe" ]]; then
    if [[ ! -f "$tarball" ]]; then
      echo "==> Download Bun $BUN_VERSION for windows/$arch"
      curl -fL --retry 3 --retry-delay 2 --connect-timeout 20 --max-time 600 \
        "https://registry.npmjs.org/$package/-/$(basename "$package")-$BUN_VERSION.tgz" \
        -o "$tarball"
    fi
    actual="sha512-$(openssl dgst -sha512 -binary "$tarball" | openssl base64 -A)"
    if [[ "$actual" != "$expected" ]]; then
      echo "Bun tarball integrity mismatch for $package@$BUN_VERSION" >&2
      exit 1
    fi
    mkdir -p "$BUN_CACHE/$arch"
    tar -xOf "$tarball" package/bin/bun.exe > "$BUN_CACHE/$arch/bun.exe"
  fi
  cp "$BUN_CACHE/$arch/bun.exe" "$out"
}

package_arch() {
  local arch="$1"
  local gui="$BUILD/QKA-windows-$arch.exe"
  local cli="$BUILD/QKA-cli-windows-$arch.exe"
  local stage="$DIST/QKA-$VERSION-windows-$arch"
  local archive="$DIST/QKA-$VERSION-windows-$arch.zip"

  if [[ ! -f "$gui" || ! -f "$cli" ]]; then
    echo "missing Windows binaries for $arch; run make build-windows build-windows-cli" >&2
    exit 2
  fi

  mkdir -p "$stage/adapters" "$stage/provider-plugins"
  # Keep this path narrowly scoped to generated package contents; never remove
  # DIST itself or a user-selected build directory.
  find "$stage" -mindepth 1 -maxdepth 1 -type f -delete
  find "$stage/adapters" -mindepth 1 -delete
  find "$stage/provider-plugins" -mindepth 1 -delete

  cp "$gui" "$stage/QKA.exe"
  cp "$cli" "$stage/QKA-cli.exe"
  cp "$ROOT/packaging/windows/README.txt" "$stage/README.txt"
  for adapter in "$ROOT"/adapters/*; do
    [[ -d "$adapter" ]] || continue
    [[ "$(basename "$adapter")" == "_template" ]] && continue
    cp -R "$adapter" "$stage/adapters/"
  done
  find "$stage/adapters" -name ".DS_Store" -delete
  for plugin in "$ROOT"/provider-plugins/*; do
    [[ -d "$plugin" ]] || continue
    cp -R "$plugin" "$stage/provider-plugins/"
  done
  find "$stage/provider-plugins" -name ".DS_Store" -delete
  prepare_bun "$arch" "$stage/bun.exe"

  cp "$gui" "$DIST/QKA-windows-$arch.exe"
  cp "$cli" "$DIST/QKA-cli-windows-$arch.exe"
  rm -f "$archive"
  (
    cd "$DIST"
    zip -qry "$(basename "$archive")" "$(basename "$stage")"
  )
  echo "  windows/$arch  $archive"
}

: > "$SUMMARY"
for arch in "${archs[@]}"; do
  package_arch "$arch"
  (
    cd "$DIST"
    shasum -a 256 "QKA-$VERSION-windows-$arch.zip"
  ) >> "$SUMMARY"
done

echo ""
echo "==> Windows packages complete"
echo "    SHA256SUMS: $SUMMARY"
