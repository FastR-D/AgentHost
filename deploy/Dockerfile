# ── Stage 1: build the web UI ─────────────────────────────────────────────────
FROM node:20-slim AS web-builder

WORKDIR /build/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY web/ ./
RUN npm run build


# ── Stage 2: build the Go binary ─────────────────────────────────────────────
# Build on the build host's native arch (BUILDPLATFORM) and cross-compile via
# GOOS/GOARCH — far faster than running `go build` under QEMU emulation.
FROM --platform=$BUILDPLATFORM golang:1.22-bookworm AS go-builder

ARG TARGETOS=linux
ARG TARGETARCH

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=web-builder /build/web/dist ./web/dist

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w" \
    -o /agenthost ./cmd/agenthost


# ── Stage 3: runtime — ubuntu 24.04, full dev environment ─────────────────────
# One image for both roles:
#   Web console:  docker compose up                          (AGENTHOST_LOCAL_NODE=off)
#   Node role:    docker compose -f docker-compose.node.yml  (sets AGENTHOST_HUB)
# Codex CLI is always present; whether it runs depends on the role.
FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates curl git \
      bubblewrap \
      python3 python3-pip \
      build-essential \
      procps iproute2 \
      sudo \
 && apt-get clean && rm -rf /var/lib/apt/lists/*

# Node.js 20 from NodeSource
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
 && apt-get install -y --no-install-recommends nodejs \
 && apt-get clean && rm -rf /var/lib/apt/lists/*

# Codex CLI + platform-native binary.
# npm 10.x silently skips platform-alias optionalDependencies,
# so install the main package then the arch-specific one explicitly.
RUN npm install -g --no-audit --no-fund @openai/codex \
 && node -e " \
      const p=require('/usr/local/lib/node_modules/@openai/codex/package.json'); \
      const key='@openai/codex-linux-'+process.arch; \
      const val=(p.optionalDependencies||{})[key]; \
      if(val) process.stdout.write(key+'@'+val); \
    " | xargs -r npm install -g --no-audit --no-fund \
 && ln -sf /usr/local/lib/node_modules/@openai/codex/bin/codex.js /usr/local/bin/codex

COPY --from=go-builder /agenthost /usr/local/bin/agenthost
COPY deploy/entrypoint.sh /usr/local/bin/entrypoint.sh

ENV AGENTHOST_ADDR=:8080 \
    AGENTHOST_DATA=/data \
    AGENTHOST_WORKSPACE=/workspace \
    AGENTHOST_SKIP_INSTALL=1

VOLUME ["/data", "/workspace", "/root/.codex"]
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s \
  CMD curl -fsS http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD []
