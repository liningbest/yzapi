#!/usr/bin/env bash
# Build the yzapi image for the server's architecture (default linux/amd64) and
# optionally export it as a tar for 1Panel 镜像 → 导入.
#   ./scripts/build-image.sh                # yzapi/gateway:<git describe>, load into local docker
#   ./scripts/build-image.sh --save         # also write dist/yzapi-<version>-amd64.tar.gz
#   PLATFORM=linux/arm64 ./scripts/build-image.sh
#   IMAGE=registry.example.com/team/yzapi ./scripts/build-image.sh --push
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION=${VERSION:-$(git describe --tags --always 2>/dev/null || echo dev)}
IMAGE=${IMAGE:-yzapi/gateway}
PLATFORM=${PLATFORM:-linux/amd64}
MODE=${1:---load}
case "$MODE" in
  --load) docker buildx build --platform "$PLATFORM" --build-arg VERSION="$VERSION" -t "$IMAGE:$VERSION" -t "$IMAGE:latest" --load . ;;
  --push) docker buildx build --platform "$PLATFORM" --build-arg VERSION="$VERSION" -t "$IMAGE:$VERSION" -t "$IMAGE:latest" --push . ;;
  --save)
    docker buildx build --platform "$PLATFORM" --build-arg VERSION="$VERSION" -t "$IMAGE:$VERSION" -t "$IMAGE:latest" --load .
    mkdir -p dist
    OUT="dist/yzapi-${VERSION}-${PLATFORM##*/}.tar.gz"
    docker save "$IMAGE:$VERSION" | gzip > "$OUT"
    echo "saved $OUT ($(du -h "$OUT" | cut -f1)); import it in 1Panel: 容器 → 镜像 → 导入" ;;
  *) echo "usage: $0 [--load|--push|--save]" >&2; exit 2 ;;
esac
echo "built $IMAGE:$VERSION for $PLATFORM"
