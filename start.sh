#!/usr/bin/env bash
# AgentHost launcher: installs missing dependencies, builds, and starts the server.
#
#   ./start.sh                 install deps if needed, build if sources changed, run in foreground
#   ./start.sh start           start in background (daemonize), print URL
#   ./start.sh stop            stop background process
#   ./start.sh restart         stop then start in background
#   ./start.sh logs            tail the log file
#   ./start.sh status          show whether background process is running
#   ./start.sh --rebuild       force rebuild, then run in foreground
#   ./start.sh --install-only  install deps and build only
#   ./start.sh node --hub URL --token T [--name N] [--workspace DIR]
#                              run as a task node (dials out, can sit behind NAT)
#
# Configuration from .env or environment variables:
#   AGENTHOST_ADDR           listen address                 (default 0.0.0.0:80)
#   AGENTHOST_TOKEN          access token                   (default: auto-generated)
#   AGENTHOST_AUTH=none      disable authentication
#   AGENTHOST_DATA           data directory                 (default ./data)
#   AGENTHOST_WORKSPACE      default task working directory (default ./workspace)
#   AGENTHOST_LOCAL_NODE=off web server does not run tasks itself
#   AGENTHOST_HUB / AGENTHOST_NODE_NAME   node role settings
#   GO_VERSION / NODE_MAJOR  toolchain versions             (default 1.22.10 / 20)
#   AGENTHOST_SKIP_INSTALL=1 never try to install anything
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

PID_FILE="$ROOT/agenthost.pid"
LOG_FILE="$ROOT/agenthost.log"

# ── daemon helpers ──────────────────────────────────────────────────────────────

_daemon_start() {
  if [[ -f "$PID_FILE" ]] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
    echo "[agenthost] already running (pid $(cat "$PID_FILE"))"
    return 0
  fi
  export AGENTHOST_DATA="${AGENTHOST_DATA:-$ROOT/data}"
  export AGENTHOST_WORKSPACE="${AGENTHOST_WORKSPACE:-$ROOT/workspace}"
  mkdir -p "$AGENTHOST_DATA" "$AGENTHOST_WORKSPACE"
  nohup "$ROOT/bin/agenthost" >> "$LOG_FILE" 2>&1 &
  echo $! > "$PID_FILE"
  echo "[agenthost] started (pid $!), waiting for server..."
  for i in $(seq 1 20); do
    if grep -q "AgentHost is running" "$LOG_FILE" 2>/dev/null; then break; fi
    sleep 0.5
  done
  grep -E "Open:|Token:|error|Error" "$LOG_FILE" 2>/dev/null | tail -5
}

_daemon_stop() {
  if [[ -f "$PID_FILE" ]] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
    kill "$(cat "$PID_FILE")" && rm -f "$PID_FILE" && echo "[agenthost] stopped"
  else
    echo "[agenthost] not running"
  fi
}

_daemon_status() {
  if [[ -f "$PID_FILE" ]] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
    echo "[agenthost] running (pid $(cat "$PID_FILE"))"
  else
    echo "[agenthost] not running"
  fi
}

# ── argument parsing ────────────────────────────────────────────────────────────

REBUILD=0
INSTALL_ONLY=0
ROLE=web
NODE_ARGS=()

case "${1:-}" in
  start)
    DAEMON=1; shift ;;
  stop)
    # stop doesn't need build; load env for PID_FILE path consistency
    if [[ -f "$ROOT/.env" ]]; then set -a; source "$ROOT/.env"; set +a; fi
    _daemon_stop; exit 0 ;;
  restart)
    if [[ -f "$ROOT/.env" ]]; then set -a; source "$ROOT/.env"; set +a; fi
    _daemon_stop; sleep 1
    DAEMON=1; shift || true ;;
  logs)
    tail -f "$LOG_FILE"; exit 0 ;;
  status)
    _daemon_status; exit 0 ;;
  node)
    ROLE=node; shift
    for arg in "$@"; do
      case "$arg" in
        --rebuild) REBUILD=1 ;;
        --install-only) INSTALL_ONLY=1 ;;
        *) NODE_ARGS+=("$arg") ;;
      esac
    done
    set -- ;;
  *)
    DAEMON=0 ;;
esac

for arg in "$@"; do
  case "$arg" in
    --rebuild) REBUILD=1 ;;
    --install-only) INSTALL_ONLY=1 ;;
    start) DAEMON=1 ;;
    -h|--help) sed -n '2,28p' "$0"; exit 0 ;;
    *) echo "unknown option: $arg" >&2; exit 2 ;;
  esac
done

DAEMON="${DAEMON:-0}"

# ── env & toolchain ─────────────────────────────────────────────────────────────

_prepare_env() {
  if [[ -f "$ROOT/.env" ]]; then
    set -a; source "$ROOT/.env"; set +a  # shellcheck disable=SC1091
  fi
}
_prepare_env

GO_VERSION="${GO_VERSION:-1.22.10}"
NODE_MAJOR="${NODE_MAJOR:-20}"
TOOLS="$ROOT/.tools"
mkdir -p "$TOOLS"
export PATH="$TOOLS/go/bin:$TOOLS/node/bin:$HOME/.local/bin:/usr/local/go/bin:$PATH"

log()  { printf '\033[1;34m[agenthost]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[agenthost]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[agenthost]\033[0m %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

SUDO=""
if [[ $(id -u) -ne 0 ]] && have sudo; then SUDO="sudo"; fi

arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo amd64 ;;
    aarch64|arm64) echo arm64 ;;
    *) die "unsupported CPU architecture: $(uname -m)" ;;
  esac
}
os() {
  case "$(uname -s)" in
    Linux) echo linux ;;
    Darwin) echo darwin ;;
    *) die "unsupported OS: $(uname -s)" ;;
  esac
}

fetch() {
  if have curl; then curl -fsSL --retry 3 -o "$2" "$1"
  elif have wget; then wget -q -O "$2" "$1"
  else die "need curl or wget to download $1"; fi
}

pkg_install() {
  [[ "${AGENTHOST_SKIP_INSTALL:-}" == 1 ]] && return 1
  if have apt-get; then $SUDO apt-get update -qq && DEBIAN_FRONTEND=noninteractive $SUDO apt-get install -y -qq "$@"
  elif have apk; then $SUDO apk add --no-cache "$@"
  elif have dnf; then $SUDO dnf install -y -q "$@"
  elif have yum; then $SUDO yum install -y -q "$@"
  elif have brew; then brew install "$@"
  else return 1; fi
}

ensure_basics() {
  local missing=()
  have curl || have wget || missing+=(curl)
  have tar || missing+=(tar)
  have git || missing+=(git)
  [[ -e /etc/ssl/certs/ca-certificates.crt || "$(os)" == darwin ]] || missing+=(ca-certificates)
  if ((${#missing[@]})); then
    log "installing system packages: ${missing[*]}"
    pkg_install "${missing[@]}" || warn "could not install ${missing[*]}; continuing"
  fi
}

version_ge() {
  [[ "$(printf '%s\n%s\n' "$2" "$1" | sort -V | head -1)" == "$2" ]]
}

ensure_go() {
  if have go; then
    local v; v="$(go env GOVERSION 2>/dev/null | sed 's/^go//')"
    if version_ge "${v:-0}" "1.21"; then return; fi
    warn "Go $v is too old (need >= 1.21)"
  fi
  [[ "${AGENTHOST_SKIP_INSTALL:-}" == 1 ]] && die "Go >= 1.21 is required"
  log "installing Go $GO_VERSION into $TOOLS/go"
  local tgz="$TOOLS/go.tgz"
  fetch "https://go.dev/dl/go${GO_VERSION}.$(os)-$(arch).tar.gz" "$tgz"
  rm -rf "$TOOLS/go" && tar -C "$TOOLS" -xzf "$tgz" && rm -f "$tgz"
}

ensure_node() {
  if have node && have npm; then
    local major; major="$(node -p 'process.versions.node.split(".")[0]')"
    if (( major >= 18 )); then return; fi
    warn "Node $(node -v) is too old (need >= 18)"
  fi
  [[ "${AGENTHOST_SKIP_INSTALL:-}" == 1 ]] && die "Node.js >= 18 is required"
  log "installing Node.js $NODE_MAJOR into $TOOLS/node"
  local index ver a; a="$(arch)"; [[ "$a" == amd64 ]] && a=x64
  index="$(mktemp)"
  fetch "https://nodejs.org/dist/latest-v${NODE_MAJOR}.x/SHASUMS256.txt" "$index"
  ver="$(grep -o "node-v[0-9.]*-$(os)-${a}.tar.gz" "$index" | head -1)"
  rm -f "$index"
  [[ -n "$ver" ]] || die "could not find a Node.js $NODE_MAJOR build for $(os)-$a"
  fetch "https://nodejs.org/dist/latest-v${NODE_MAJOR}.x/$ver" "$TOOLS/node.tgz"
  rm -rf "$TOOLS/node" && mkdir -p "$TOOLS/node"
  tar -C "$TOOLS/node" --strip-components=1 -xzf "$TOOLS/node.tgz" && rm -f "$TOOLS/node.tgz"
}

isOff() { [[ "${1,,}" == "off" || "${1,,}" == "false" || "$1" == "0" ]]; }

ensure_codex() {
  isOff "${AGENTHOST_LOCAL_NODE:-}" && return
  local bin="${CODEX_BIN:-codex}"
  if have "$bin"; then return; fi
  [[ "${AGENTHOST_SKIP_INSTALL:-}" == 1 ]] && { warn "Codex CLI not found; tasks will fail until it is installed"; return; }
  log "installing Codex CLI (npm i -g @openai/codex)"
  if ! npm install -g --no-audit --no-fund @openai/codex >/dev/null 2>&1; then
    npm install -g --no-audit --no-fund --prefix "$TOOLS/node" @openai/codex >/dev/null \
      || warn "could not install the Codex CLI; install it manually: npm i -g @openai/codex"
  fi
}

check_codex_auth() {
  isOff "${AGENTHOST_LOCAL_NODE:-}" && return
  local bin="${CODEX_BIN:-codex}"
  have "$bin" || return 0
  local home="${CODEX_HOME:-$HOME/.codex}"
  if [[ -z "${OPENAI_API_KEY:-}" && ! -f "$home/auth.json" && ! -f "$home/config.toml" ]]; then
    warn "Codex does not look configured (no OPENAI_API_KEY, $home/auth.json or config.toml)."
    warn "Run 'codex login' or set OPENAI_API_KEY, or configure it via the web settings page."
  fi
}

src_hash() {
  find "$@" -type f \( -name '*.go' -o -name '*.ts' -o -name '*.tsx' -o -name '*.css' -o -name '*.html' \
    -o -name '*.json' -o -name '*.js' -o -name '*.md' -o -name 'go.mod' -o -name 'go.sum' \) \
    -not -path '*/node_modules/*' -not -path '*/dist/*' -not -path './data/*' -not -path './workspace/*' \
    -not -path './.tools/*' -print0 2>/dev/null | sort -z | xargs -0 cat 2>/dev/null | cksum | cut -d' ' -f1
}

build_web() {
  local stamp="$ROOT/web/dist/.build-hash" h
  h="$(cd "$ROOT" && src_hash web/src web/index.html web/package.json web/vite.config.ts web/tailwind.config.js)"
  if [[ $REBUILD == 0 && -f "$ROOT/web/dist/index.html" && -f "$stamp" && "$(cat "$stamp")" == "$h" ]]; then
    log "web UI is up to date"; return
  fi
  # No web source (release package) and dist stamp exists — skip.
  if [[ $REBUILD == 0 && -f "$stamp" ]] && \
     ! find "$ROOT/web" -maxdepth 2 -name '*.ts' -o -name '*.tsx' 2>/dev/null | grep -q .; then
    log "no web source found; using pre-built UI"; return
  fi
  if (cd "$ROOT/web" && [[ -d node_modules ]] && npm ls --depth=0 >/dev/null 2>&1); then
    log "web dependencies already installed"
  else
    log "installing web dependencies"
    (cd "$ROOT/web" && { [[ -f package-lock.json ]] && npm ci --no-audit --no-fund --prefer-offline \
      || npm install --no-audit --no-fund --prefer-offline; }) >/dev/null
  fi
  log "building web UI"
  (cd "$ROOT/web" && npx tsc --noEmit -p tsconfig.json && npx vite build >/dev/null)
  touch "$ROOT/web/dist/.gitkeep"
  echo "$h" > "$stamp"
}

build_go() {
  local stamp="$ROOT/bin/.build-hash" h
  h="$(cd "$ROOT" && src_hash cmd internal tasks go.mod go.sum assets.go)-$(cat "$ROOT/web/dist/.build-hash" 2>/dev/null)"
  if [[ $REBUILD == 0 && -x "$ROOT/bin/agenthost" && -f "$stamp" && "$(cat "$stamp")" == "$h" ]]; then
    log "server binary is up to date"; return
  fi
  # No source found (release package) — use pre-built binary as-is.
  if [[ $REBUILD == 0 && -x "$ROOT/bin/agenthost" ]] && \
     ! find "$ROOT" -maxdepth 3 -name '*.go' -not -path '*/.tools/*' -quit 2>/dev/null | grep -q .; then
    log "no source found; using pre-built binary at $ROOT/bin/agenthost"; return
  fi
  log "building server"
  mkdir -p "$ROOT/bin"
  (cd "$ROOT" && CGO_ENABLED=0 GOOS="$(os)" GOARCH="$(arch)" GOFLAGS="${GOFLAGS:--mod=mod}" \
    go build -trimpath -ldflags "-s -w" -o bin/agenthost ./cmd/agenthost)
  echo "$h" > "$stamp"
}

# ── main ────────────────────────────────────────────────────────────────────────

ensure_basics
ensure_go
ensure_node
ensure_codex
check_codex_auth
log "go $(go env GOVERSION) · node $(node -v) · $( (${CODEX_BIN:-codex} --version 2>/dev/null || echo 'codex: not installed') | head -1)"

build_web
build_go

if [[ $INSTALL_ONLY == 1 ]]; then
  log "install complete: $ROOT/bin/agenthost"; exit 0
fi

if [[ $ROLE == node ]]; then
  export AGENTHOST_DATA="${AGENTHOST_DATA:-$ROOT/node-data}"
  export AGENTHOST_WORKSPACE="${AGENTHOST_WORKSPACE:-$ROOT/workspace}"
  mkdir -p "$AGENTHOST_DATA" "$AGENTHOST_WORKSPACE"
  log "starting AgentHost node (connects out to ${AGENTHOST_HUB:-the --hub address})"
  exec "$ROOT/bin/agenthost" node "${NODE_ARGS[@]}"
fi

export AGENTHOST_DATA="${AGENTHOST_DATA:-$ROOT/data}"
export AGENTHOST_WORKSPACE="${AGENTHOST_WORKSPACE:-$ROOT/workspace}"
mkdir -p "$AGENTHOST_DATA" "$AGENTHOST_WORKSPACE"

if [[ $DAEMON == 1 ]]; then
  _daemon_start
  exit 0
fi

log "starting AgentHost on ${AGENTHOST_ADDR:-0.0.0.0:80}"
exec "$ROOT/bin/agenthost"
