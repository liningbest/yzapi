#!/usr/bin/env bash
# Cross-compile a static Linux binary (frontend embedded) and pack it with
# Dockerfile.prebuilt and the deploy files. The server needs neither Node nor Go and the
# image build takes seconds: useful for small VPS instances where `go build` runs out of
# memory.
#   ./scripts/package-release.sh [output.tar.gz]        # default dist/yzapi-<version>-linux-amd64.tar.gz
#   GOARCH=arm64 ./scripts/package-release.sh
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION=${VERSION:-$(git describe --tags --always 2>/dev/null || echo dev)}
GOARCH=${GOARCH:-amd64}
OUT=${1:-dist/yzapi-${VERSION}-linux-${GOARCH}.tar.gz}
mkdir -p "$(dirname "$OUT")"
(cd web && pnpm install --frozen-lockfile >/dev/null && pnpm build >/dev/null)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/yzapi"
GOOS=linux GOARCH=$GOARCH CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o "$TMP/yzapi/yzapi" ./cmd/yzapi
cp Dockerfile.prebuilt "$TMP/yzapi/Dockerfile.prebuilt"
cp -R deploy "$TMP/yzapi/deploy"
echo "$VERSION" > "$TMP/yzapi/VERSION"
tar -C "$TMP" -czf "$OUT" yzapi
echo "packed $OUT ($(du -h "$OUT" | cut -f1), version $VERSION, linux/$GOARCH)"
echo "server: tar xzf $(basename "$OUT") && cd yzapi && docker build -f Dockerfile.prebuilt -t yzapi/gateway:$VERSION ."
