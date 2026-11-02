#!/usr/bin/env bash
# Collect already-built macOS DMGs and Windows ZIPs into the self-update feed.
# This script only reads release artifacts and atomically writes feed.json and
# SHA256SUMS; it never rebuilds or deletes application bundles.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST="${DIST:-$ROOT/dist}"
VERSION="${VERSION:-$(git -C "$ROOT" describe --tags --always 2>/dev/null || echo dev)}"
VERSION="${VERSION#v}"
BASE_URL="${QKA_RELEASE_BASE_URL:-https://github.com/sljdxde/quota-keepalive/releases/latest/download}"
REQUIRE_PLATFORMS="${QKA_REQUIRE_PLATFORMS:-darwin/arm64,darwin/amd64,windows/amd64,windows/arm64}"

if [[ ! -d "$DIST" ]]; then
  echo "release artifact directory does not exist: $DIST" >&2
  exit 2
fi

args=()
artifacts=()

add_artifact() {
  local platform="$1" path="$2"
  local existing
  args+=(--artifact "$platform=$path")
  if [[ ${#artifacts[@]} -gt 0 ]]; then
    for existing in "${artifacts[@]}"; do
      if [[ "$existing" == "$path" ]]; then
        return
      fi
    done
  fi
  artifacts+=("$path")
}

shopt -s nullglob
for path in "$DIST/dmg/QKA-$VERSION-"*.dmg; do
  [[ -f "$path" ]] || continue
  case "$(basename "$path")" in
    *-universal.dmg)
      add_artifact darwin/arm64 "$path"
      add_artifact darwin/amd64 "$path"
      ;;
    *-arm64.dmg) add_artifact darwin/arm64 "$path" ;;
    *-amd64.dmg) add_artifact darwin/amd64 "$path" ;;
    *)
      echo "unsupported macOS artifact name: $path" >&2
      exit 2
      ;;
  esac
done

for path in "$DIST/QKA-$VERSION-windows-"*.zip; do
  [[ -f "$path" ]] || continue
  case "$(basename "$path")" in
    *-windows-arm64.zip) add_artifact windows/arm64 "$path" ;;
    *-windows-amd64.zip) add_artifact windows/amd64 "$path" ;;
    *)
      echo "unsupported Windows artifact name: $path" >&2
      exit 2
      ;;
  esac
done

if [[ ${#args[@]} -eq 0 ]]; then
  echo "no QKA $VERSION release artifacts found under $DIST" >&2
  exit 2
fi

(
  cd "$ROOT"
  go run ./cmd/releasefeed \
    --version "$VERSION" \
    --base-url "$BASE_URL" \
    --require-platforms "$REQUIRE_PLATFORMS" \
    --out "$DIST/feed.json" \
    "${args[@]}"
)

shasum -a 256 "${artifacts[@]}" > "$DIST/SHA256SUMS"
echo "SHA256SUMS: $DIST/SHA256SUMS"
echo "feed.json:  $DIST/feed.json"
