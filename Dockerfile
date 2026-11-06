# ── Stage 1: build the web UI ─────────────────────────────────────────────────
FROM node:20-slim AS web-builder

WORKDIR /build/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY web/ ./
RUN npm run build


# ── Stage 2: build the Go binary ─────────────────────────────────────────────
# TARGETOS / TARGETARCH are injected by BuildKit from `docker build --platform`.
# Plain `docker build .` builds for the host arch; CI passes
# `--platform linux/amd64` to produce the x86 linux release binary.
FROM --platform=$BUILDPLATFORM golang:1.22-bookworm AS go-builder

ARG TARGETOS=linux
ARG TARGETARCH

WORKDIR /build

# Download Go modules first for better layer caching.
COPY go.mod go.sum ./
RUN go mod download

# Copy source and the pre-built web dist (required by //go:embed all:web/dist).
COPY . .
COPY --from=web-builder /build/web/dist ./web/dist

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w" \
    -o /agenthost ./cmd/agenthost


# ── Stage 3: minimal runtime image ────────────────────────────────────────────
# We copy Node.js from the official image instead of apt-get install,
# which avoids pulling 300+ Debian npm packages and their network round-trips.
FROM node:20-slim AS node-provider
# npm 10.x skips platform-alias optionalDependencies automatically.
# We install the main package first, then read its optionalDependencies
# to install the correct platform binary (e.g. codex-linux-arm64).
RUN npm install -g --no-audit --no-fund @openai/codex \
 && node -e " \
      const p=require('/usr/local/lib/node_modules/@openai/codex/package.json'); \
      const key='@openai/codex-linux-'+process.arch; \
      const val=(p.optionalDependencies||{})[key]; \
      if(val) process.stdout.write(key+'@'+val); \
    " | xargs -r npm install -g --no-audit --no-fund

FROM debian:bookworm-slim

# Runtime deps (small list – no nodejs/npm from apt):
#   ca-certificates – HTTPS from the binary
#   curl            – healthcheck
#   git             – Codex needs it for diffs / commits
#   bubblewrap      – Codex sandbox (workspace-write mode)
#   python3         – some Codex tasks
#   procps          – ps, used by task cleanup logic
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      ca-certificates curl git bubblewrap python3 procps \
 && apt-get clean && rm -rf /var/lib/apt/lists/*

# Copy Node.js runtime + pre-installed codex CLI from node-provider.
# We copy node_modules directly and recreate the /usr/local/bin/codex
# symlink manually — a plain COPY dereferences symlinks, which breaks
# codex's require.resolve-based platform-package lookup.
COPY --from=node-provider /usr/local/bin/node            /usr/local/bin/node
COPY --from=node-provider /usr/local/lib/node_modules    /usr/local/lib/node_modules
RUN ln -sf /usr/local/lib/node_modules/@openai/codex/bin/codex.js /usr/local/bin/codex \
 && chmod +x /usr/local/bin/codex

COPY --from=go-builder /agenthost /usr/local/bin/agenthost
COPY entrypoint.sh /usr/local/bin/entrypoint.sh

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
