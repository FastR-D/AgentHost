"""
AgentHost deployment script.

Usage:
    fab deploy          build locally then deploy to server
    fab deploy --skip-build   deploy without rebuilding
    fab stop            stop the server
    fab logs            tail the remote log
    fab status          show running status
    fab token           print current access token and URL
    fab node-token      print the node join token

Requirements:
    pip install fabric

Configuration in deploy/.fab.env:
    AGENTHOST_HOST          SSH target, e.g. root@8.219.191.172
    AGENTHOST_DIR           remote install directory (default /opt/agenthost)
    AGENTHOST_SSH_KEY       path to SSH private key (e.g. ~/.ssh/id_ed25519)
    AGENTHOST_SSH_PASSWORD  or use password auth
"""

import os
import subprocess
import sys
import time
from pathlib import Path

from fabric import Connection, task

# ── config ────────────────────────────────────────────────────────────────────

def _load_env():
    env_file = Path(__file__).parent / ".fab.env"
    if env_file.exists():
        for line in env_file.read_text().splitlines():
            line = line.strip()
            if line and not line.startswith("#") and "=" in line:
                k, v = line.split("=", 1)
                os.environ.setdefault(k.strip(), v.strip())

_load_env()

DEFAULT_HOST    = os.environ.get("AGENTHOST_HOST", "root@8.219.191.172")
REMOTE_DIR      = os.environ.get("AGENTHOST_DIR",  "/opt/agenthost")
DEPLOY_KEY      = os.environ.get("AGENTHOST_SSH_KEY", "")
DEPLOY_PASSWORD = os.environ.get("AGENTHOST_SSH_PASSWORD", "")
REPO_ROOT       = Path(__file__).parent.parent  # project root


def _conn(host=None):
    host = host or DEFAULT_HOST
    kwargs = {}
    if DEPLOY_KEY:
        kwargs["connect_kwargs"] = {"key_filename": os.path.expanduser(DEPLOY_KEY)}
    elif DEPLOY_PASSWORD:
        kwargs["connect_kwargs"] = {"password": DEPLOY_PASSWORD}
    return Connection(host, **kwargs)


def _build():
    """Build linux/amd64 binary locally via Docker."""
    print("==> building linux/amd64 binary locally...")
    result = subprocess.run(
        ["bash", "build.sh"],
        cwd=str(REPO_ROOT),
    )
    if result.returncode != 0:
        print("build failed", file=sys.stderr)
        sys.exit(1)
    print("==> build done")


# ── tasks ─────────────────────────────────────────────────────────────────────

@task(default=True)
def deploy(c, host=None, skip_build=False):
    """Build locally then upload binary and restart the server."""
    if not skip_build:
        _build()

    c = _conn(host)
    print(f"==> deploying to {host or DEFAULT_HOST}  dir={REMOTE_DIR}")

    # ensure dir exists
    c.run(f"mkdir -p {REMOTE_DIR}/bin", hide=True)

    # upload the freshly built binary
    c.put(str(REPO_ROOT / "bin" / "agenthost-linux-amd64"), f"{REMOTE_DIR}/bin/agenthost")
    c.run(f"chmod +x {REMOTE_DIR}/bin/agenthost", hide=True)

    # upload start.sh (picks up any changes)
    c.put(str(REPO_ROOT / "start.sh"), f"{REMOTE_DIR}/start.sh")
    c.run(f"chmod +x {REMOTE_DIR}/start.sh", hide=True)

    # copy .env.example if no .env exists yet
    c.run(
        f"[ -f {REMOTE_DIR}/.env ] || "
        f"cp {REMOTE_DIR}/.env.example {REMOTE_DIR}/.env 2>/dev/null || true",
        hide=True, warn=True,
    )

    # stop old process
    c.run(f"pkill -f '{REMOTE_DIR}/bin/agenthost' || true", hide=True, warn=True)
    time.sleep(1)

    # start detached (setsid so it survives SSH session close)
    c.run(
        f"cd {REMOTE_DIR} && "
        f"rm -f agenthost.log && "
        f"setsid ./bin/agenthost >> agenthost.log 2>&1 </dev/null & "
        f"sleep 2 && pgrep -n -f '{REMOTE_DIR}/bin/agenthost' > agenthost.pid && "
        f"echo pid=$(cat agenthost.pid)",
        echo=True,
    )

    time.sleep(3)
    c.run(f"tail -8 {REMOTE_DIR}/agenthost.log", echo=True)
    r = c.run("curl -fsS http://127.0.0.1:80/healthz && echo ' healthz OK'", warn=True)
    if "OK" not in (r.stdout + r.stderr):
        print("WARNING: healthz failed — check logs with: fab logs")
    else:
        token(c, host=host or DEFAULT_HOST)


@task
def stop(c, host=None):
    """Stop the running AgentHost process."""
    c = _conn(host)
    c.run(f"pkill -f '{REMOTE_DIR}/bin/agenthost' || true", warn=True)
    c.run(f"rm -f {REMOTE_DIR}/agenthost.pid", hide=True, warn=True)
    print("stopped")


@task
def logs(c, host=None):
    """Tail the remote log."""
    c = _conn(host)
    c.run(f"tail -f {REMOTE_DIR}/agenthost.log", pty=True)


@task
def status(c, host=None):
    """Show running status and last log lines."""
    c = _conn(host)
    pid_running = c.run(
        f"kill -0 $(cat {REMOTE_DIR}/agenthost.pid 2>/dev/null) 2>/dev/null && echo running || echo stopped",
        hide=True, warn=True,
    ).stdout.strip()
    print(f"process: {pid_running}")
    c.run(f"tail -10 {REMOTE_DIR}/agenthost.log", warn=True)


@task
def token(c, host=None):
    """Print the current access token and open URL."""
    c = _conn(host)
    tok = c.run(
        f"cat {REMOTE_DIR}/data/token 2>/dev/null || "
        f"grep 'Token:' {REMOTE_DIR}/agenthost.log 2>/dev/null | tail -1",
        hide=True, warn=True,
    ).stdout.strip().replace("Token:", "").strip()
    host_part = (host or DEFAULT_HOST).split("@")[-1]
    print(f"\n  Token: {tok}")
    print(f"  Open:  http://{host_part}/?token={tok}\n")


@task
def node_token(c, host=None):
    """Print the node join token (for connecting remote nodes)."""
    c = _conn(host)
    tok = c.run(f"cat {REMOTE_DIR}/data/node-token 2>/dev/null", hide=True, warn=True).stdout.strip()
    host_part = (host or DEFAULT_HOST).split("@")[-1]
    print(f"\n  Node token: {tok}")
    print(f"\n  Connect with:")
    print(f"  ./start.sh node --hub http://{host_part} --token {tok} --name my-machine\n")
