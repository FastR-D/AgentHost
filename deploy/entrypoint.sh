#!/bin/sh
# AgentHost container entrypoint.
#
# Web server mode (default):
#   docker run agenthost
#
# Node mode (内网机器主动连公网 Web 控制台):
#   docker run -e AGENTHOST_HUB=https://gate.example.com \
#              -e AGENTHOST_NODE_TOKEN=xxx agenthost
#
# Shell / exec passthrough (for docker run ... sh -c "..." or docker exec):
#   docker run agenthost sh -c "codex --version"

# Pass through shell commands directly without wrapping in agenthost.
case "${1:-}" in
  sh|bash|/bin/sh|/bin/bash) exec "$@" ;;
esac

if [ -n "${AGENTHOST_HUB:-}" ]; then
    echo "[agenthost] node mode → hub: $AGENTHOST_HUB"
    exec /usr/local/bin/agenthost node
fi

exec /usr/local/bin/agenthost "$@"
