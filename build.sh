#!/usr/bin/env bash
# Build linux/amd64 binary using the existing multi-stage Dockerfile.
# The result is extracted to bin/agenthost.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo dev)
DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
IMAGE="agenthost-build-amd64:${COMMIT}"

echo "==> Building linux/amd64 binary (commit $COMMIT)..."

# Build only up to the go-builder stage, with explicit cross-compile args.
# web-builder uses BUILDPLATFORM (native), go-builder cross-compiles to amd64.
docker build \
  --target go-builder \
  -f deploy/Dockerfile \
  --build-arg TARGETOS=linux \
  --build-arg TARGETARCH=amd64 \
  -t "$IMAGE" \
  .

mkdir -p bin
id=$(docker create "$IMAGE")
docker cp "$id":/agenthost bin/agenthost-linux-amd64
docker rm "$id" >/dev/null

echo "==> Done: bin/agenthost-linux-amd64 ($(du -sh bin/agenthost-linux-amd64 | cut -f1), linux/amd64)"
echo "    commit: $COMMIT"
