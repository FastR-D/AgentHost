#!/usr/bin/env bash
# AgentHost launcher: installs missing dependencies, builds, and starts the server.
#
#   ./start.sh                 install deps if needed, build if sources changed, run
#   ./start.sh --rebuild       force a rebuild of the web UI and the Go binary
#   ./start.sh --install-only  install deps and build, but do not start
#   ./start.sh node --hub URL --token T [--name N] [--workspace DIR]
#                              run this machine as a task server for a web server
#                              (it dials out, so it can sit behind NAT / a firewall)
#
# Configuration comes from environment variables (all optional), or from a
# .env file next to this script:
#   AGENTHOST_ADDR           listen address                 (default :8080)
#   AGENTHOST_TOKEN          access token                   (default: generated, saved in $AGENTHOST_DATA/token)
#   AGENTHOST_AUTH=none      disable authentication (only on trusted networks!)
#   AGENTHOST_DATA           data directory                 (default ./data)
#   AGENTHOST_WORKSPACE      default task working directory (default ./workspace)
#   AGENTHOST_WORKFLOWS      workflow library directory     (default $AGENTHOST_DATA/workflows)
#   AGENTHOST_MODEL          default Codex model            (default: Codex config)
#   AGENTHOST_SANDBOX        read-only | workspace-write | danger-full-access (default workspace-write)
#   AGENTHOST_APPROVAL_POLICY   never | on-request | untrusted (default never)
#   AGENTHOST_APPROVAL_DECISION accept | decline: reply to Codex approval requests (default decline)
#   AGENTHOST_REASONING_SUMMARY auto | concise | detailed | none | default (default auto)
#   AGENTHOST_MAX_RUNS       max concurrently running tasks (default 4)
#   AGENTHOST_LOCAL_NODE=off the web server does not run tasks itself, only remote servers do
#   AGENTHOST_NODE_TOKEN     token remote servers use to join (default: generated, $AGENTHOST_DATA/node-token)
#   AGENTHOST_HUB / AGENTHOST_NODE_NAME   node role: web server URL and this server's name
#   AGENTHOST_CODEX_CONFIG   extra codex -c overrides, separated by ';'  e.g. 'model_reasoning_effort="high"'
#   CODEX_BIN                codex executable               (default codex)
#   OPENAI_API_KEY           passed through to Codex if you authenticate with an API key
#   GO_VERSION / NODE_MAJOR  versions to install when missing (default 1.22.10 / 20)
#   AGENTHOST_SKIP_INSTALL=1 never try to install anything
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

REBUILD=0
INSTALL_ONLY=0
ROLE=web
NODE_ARGS=()
if [[ "${1:-}" == "node" ]]; then
  ROLE=node
  shift
  # Everything after "node" goes to `agenthost node` (--hub, --token, --name, --workspace…),
  # except start.sh's own options.
  for arg in "$@"; do
    case "$arg" in
      --rebuild) REBUILD=1 ;;
      --install-only) INSTALL_ONLY=1 ;;
      *) NODE_ARGS+=("$arg") ;;
    esac
  done
  set --
fi
for arg in "$@"; do
  case "$arg" in
    --rebuild) REBUILD=1 ;;
    --install-only) INSTALL_ONLY=1 ;;
    -h|--help) sed -n '2,38p' "$0"; exit 0 ;;
    *) echo "unknown option: $arg" >&2; exit 2 ;;
  esac
done

if [[ -f "$ROOT/.env" ]]; then
  set -a; # shellcheck disable=SC1091
  source "$ROOT/.env"; set +a
fi

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

fetch() { # url dest
  if have curl; then curl -fsSL --retry 3 -o "$2" "$1"
  elif have wget; then wget -q -O "$2" "$1"
  else die "need curl or wget to download $1"; fi
}

pkg_install() { # best-effort system packages
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

version_ge() { # a >= b (dotted)
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

ensure_codex() {
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
  local bin="${CODEX_BIN:-codex}"
  have "$bin" || return 0
  local home="${CODEX_HOME:-$HOME/.codex}"
  if [[ -z "${OPENAI_API_KEY:-}" && ! -f "$home/auth.json" && ! -f "$home/config.toml" ]]; then
    warn "Codex does not look configured (no $home/auth.json, $home/config.toml or OPENAI_API_KEY)."
    warn "Run 'codex login' (or 'printenv OPENAI_API_KEY | codex login --with-api-key'), or mount your ~/.codex."
  fi
}

# Hash of everything that affects a build, to skip rebuilding when nothing changed.
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
    log "web UI is up to date"
    return
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
    log "server binary is up to date"
    return
  fi
  log "building server"
  mkdir -p "$ROOT/bin"
  # Target the host explicitly: the installed Go may itself be an emulated build for another arch.
  (cd "$ROOT" && CGO_ENABLED=0 GOOS="$(os)" GOARCH="$(arch)" GOFLAGS="${GOFLAGS:--mod=mod}" \
    go build -trimpath -ldflags "-s -w" -o bin/agenthost ./cmd/agenthost)
  echo "$h" > "$stamp"
}

ensure_basics
ensure_go
ensure_node
ensure_codex
check_codex_auth
log "go $(go env GOVERSION) · node $(node -v) · $( (${CODEX_BIN:-codex} --version 2>/dev/null || echo 'codex: not installed') | head -1)"

build_web
build_go

if [[ $INSTALL_ONLY == 1 ]]; then
  log "install complete: $ROOT/bin/agenthost"
  exit 0
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
log "starting AgentHost on ${AGENTHOST_ADDR:-:8080}"
exec "$ROOT/bin/agenthost"
