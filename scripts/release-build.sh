#!/usr/bin/env bash
# Cross-compile release artifacts for api / hubd / runner / mcp.
# Usage:
#   ./scripts/release-build.sh              # VERSION from internal/version or git describe
#   ./scripts/release-build.sh v0.2.0
#   VERSION=0.2.0 OUT=dist ./scripts/release-build.sh
#
# Outputs under ${OUT:-dist/}:
#   aicloudhub_${VERSION}_${os}_${arch}.tar.gz  (or .zip on windows)
#   checksums.txt  (SHA-256)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
export CGO_ENABLED=0

VERSION="${1:-${VERSION:-}}"
if [[ -z "$VERSION" ]]; then
  if git describe --tags --exact-match HEAD >/dev/null 2>&1; then
    VERSION="$(git describe --tags --exact-match HEAD)"
  elif git describe --tags >/dev/null 2>&1; then
    VERSION="$(git describe --tags --always --dirty)"
  else
    VERSION="0.2.0-dev"
  fi
fi
VERSION="${VERSION#v}"

OUT="${OUT:-dist}"
rm -rf "$OUT"
mkdir -p "$OUT"

LDFLAGS="-s -w -X github.com/awmbtc/AI-cloudhub/internal/version.Version=${VERSION}"
BINS=(api hubd runner mcp)

# os/arch pairs
TARGETS=(
  "linux/amd64"
  "linux/arm64"
  "darwin/amd64"
  "darwin/arm64"
  "windows/amd64"
)

echo "Building release VERSION=$VERSION → $OUT"

for target in "${TARGETS[@]}"; do
  goos="${target%/*}"
  goarch="${target#*/}"
  dir="$OUT/aicloudhub_${VERSION}_${goos}_${goarch}"
  mkdir -p "$dir"
  for bin in "${BINS[@]}"; do
    ext=""
    if [[ "$goos" == "windows" ]]; then
      ext=".exe"
    fi
    echo "  $goos/$goarch $bin"
    GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags="$LDFLAGS" \
      -o "${dir}/${bin}${ext}" "./cmd/${bin}"
  done
  # bundle README + LICENSE if present
  [[ -f README.md ]] && cp README.md "$dir/"
  [[ -f LICENSE ]] && cp LICENSE "$dir/" || true
  [[ -f docs/PRODUCTION.md ]] && cp docs/PRODUCTION.md "$dir/" || true

  archive_base="aicloudhub_${VERSION}_${goos}_${goarch}"
  if [[ "$goos" == "windows" ]]; then
    (cd "$OUT" && zip -qr "${archive_base}.zip" "$(basename "$dir")")
  else
    tar -C "$OUT" -czf "$OUT/${archive_base}.tar.gz" "$(basename "$dir")"
  fi
  rm -rf "$dir"
done

# checksums (archives only)
(
  cd "$OUT"
  files=( )
  for f in *.tar.gz *.zip; do
    [[ -f "$f" ]] && files+=("$f")
  done
  if [[ ${#files[@]} -eq 0 ]]; then
    echo "no archives produced" >&2
    exit 1
  fi
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "${files[@]}" >checksums.txt
  else
    sha256sum "${files[@]}" >checksums.txt
  fi
)

echo "OK release artifacts:"
ls -la "$OUT"
cat "$OUT/checksums.txt"
