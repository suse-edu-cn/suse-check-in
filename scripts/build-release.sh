#!/usr/bin/env bash
set -euo pipefail

VERSION="${1:-${VERSION:-dev}}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="${OUT_DIR:-$ROOT/dist}"
PLATFORMS="${PLATFORMS:-linux/amd64 linux/arm64 darwin/arm64}"
LDFLAGS="-s -w -X suse-checkin/internal/version.Version=${VERSION}"

cd "$ROOT"
rm -rf "$OUT"
mkdir -p "$OUT"

for platform in $PLATFORMS; do
  os="${platform%%/*}"
  arch="${platform##*/}"
  name="suse-checkin_${VERSION}_${os}_${arch}"
  stage="$OUT/$name"
  echo "==> ${os}/${arch} -> ${name}.tar.gz"
  mkdir -p "$stage"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -buildvcs=false \
    -ldflags "$LDFLAGS" -o "$stage/suse-checkin" ./cmd/server
  cp README.md "$stage/README.md"
  tar -C "$OUT" -czf "$OUT/$name.tar.gz" "$name"
  rm -rf "$stage"
done

cd "$OUT"
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum ./*.tar.gz > sha256sums.txt
else
  shasum -a 256 ./*.tar.gz > sha256sums.txt
fi

echo "==> ${OUT}"
ls -lh "$OUT"
