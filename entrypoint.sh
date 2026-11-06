#!/bin/sh
# AgentHost container entrypoint.
#
# Web server mode (default):
#   docker run agenthost                          # listens on :8080
#
# Node mode (内网机器主动连公网 Web 控制台):
#   docker run -e AGENTHOST_HUB=https://gate.example.com \
#              -e AGENTHOST_NODE_TOKEN=xxx agenthost
#
# All AGENTHOST_* env vars are forwarded to the binary automatically.

if [ -n "${AGENTHOST_HUB:-}" ]; then
    echo "[agenthost] node mode → hub: $AGENTHOST_HUB"
    exec /usr/local/bin/agenthost node
fi

exec /usr/local/bin/agenthost "$@"
