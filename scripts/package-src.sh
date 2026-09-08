#!/usr/bin/env bash
# Build the frontend here and pack the sources plus web/dist into one archive that a
# server can build into an image with a plain `docker build` (no Node needed there).
#   ./scripts/package-src.sh [output.tar.gz]      # default: dist/yzapi-src-<version>.tar.gz
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION=$(git describe --tags --always 2>/dev/null || echo dev)
OUT=${1:-dist/yzapi-src-${VERSION}.tar.gz}
mkdir -p "$(dirname "$OUT")"
(cd web && pnpm install --frozen-lockfile >/dev/null && pnpm build >/dev/null)
test -f web/dist/index.html
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
git archive --format=tar --prefix=yzapi/ HEAD | tar -x -C "$TMP"
mkdir -p "$TMP/yzapi/web/dist" && cp -R web/dist/. "$TMP/yzapi/web/dist/"
echo "$VERSION" > "$TMP/yzapi/VERSION"
tar -C "$TMP" -czf "$OUT" yzapi
echo "packed $OUT ($(du -h "$OUT" | cut -f1), version $VERSION)"
echo "server: tar xzf $(basename "$OUT") && cd yzapi && docker build --build-arg VERSION=$VERSION -t yzapi/gateway:$VERSION ."
