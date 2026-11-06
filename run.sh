#!/usr/bin/env bash
# AgentHost launcher for release builds (no source code needed).
#
#   ./run.sh              start in background, print the access URL
#   ./run.sh stop         stop the background process
#   ./run.sh restart      stop then start
#   ./run.sh logs         tail the log
#   ./run.sh status       show whether it's running

set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN="$ROOT/agenthost"
PID_FILE="$ROOT/agenthost.pid"
LOG_FILE="$ROOT/agenthost.log"

if [[ ! -x "$BIN" ]]; then
  echo "error: $BIN not found or not executable" >&2
  exit 1
fi

# Load .env if present
if [[ -f "$ROOT/.env" ]]; then
  set -a; source "$ROOT/.env"; set +a
fi

cmd="${1:-start}"

case "$cmd" in
  start)
    if [[ -f "$PID_FILE" ]] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
      echo "already running (pid $(cat "$PID_FILE"))"
      exit 0
    fi
    nohup "$BIN" >> "$LOG_FILE" 2>&1 &
    echo $! > "$PID_FILE"
    echo "started (pid $!), waiting for server..."
    for i in $(seq 1 20); do
      if grep -q "AgentHost is running" "$LOG_FILE" 2>/dev/null; then break; fi
      sleep 0.5
    done
    grep -E "Open:|Token:|error|Error" "$LOG_FILE" 2>/dev/null | tail -5
    ;;
  stop)
    if [[ -f "$PID_FILE" ]] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
      kill "$(cat "$PID_FILE")" && rm -f "$PID_FILE" && echo "stopped"
    else
      echo "not running"
    fi
    ;;
  restart)
    "$0" stop || true
    sleep 1
    "$0" start
    ;;
  logs)
    tail -f "$LOG_FILE"
    ;;
  status)
    if [[ -f "$PID_FILE" ]] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
      echo "running (pid $(cat "$PID_FILE"))"
    else
      echo "not running"
    fi
    ;;
  *)
    echo "usage: $0 {start|stop|restart|logs|status}" >&2
    exit 1
    ;;
esac
