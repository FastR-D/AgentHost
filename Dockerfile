# AgentHost container: start.sh installs Go, Node.js and the Codex CLI, builds, and runs.
FROM debian:bookworm-slim

RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      ca-certificates curl git tar xz-utils bubblewrap python3 procps \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /opt/agenthost
COPY . .
# Install toolchains + Codex CLI and build once at image build time,
# so container start is fast. start.sh skips work that is already done.
RUN ./start.sh --install-only

ENV AGENTHOST_ADDR=:8080 \
    AGENTHOST_DATA=/data \
    AGENTHOST_WORKSPACE=/workspace
VOLUME ["/data", "/workspace", "/root/.codex"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s CMD curl -fsS http://127.0.0.1:8080/healthz || exit 1
CMD ["./start.sh"]
