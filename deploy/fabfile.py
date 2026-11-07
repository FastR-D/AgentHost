"""
AgentHost deployment script.

Usage:
    fab deploy                    # deploy latest release to default host
    fab deploy --host root@1.2.3.4
    fab rollback                  # re-run previous start.sh (binary unchanged)

Requirements:
    pip install fabric

Configuration (environment variables or .fab.env):
    AGENTHOST_HOST     SSH target, e.g. root@8.219.191.172
    AGENTHOST_DIR      remote install directory (default /opt/agenthost)
"""

import os
from pathlib import Path

from fabric import Connection, task

# ── config ───────────────────────────────────────────────────────────────────

def _load_env():
    env_file = Path(__file__).parent / ".fab.env"
    if env_file.exists():
        for line in env_file.read_text().splitlines():
            line = line.strip()
            if line and not line.startswith("#") and "=" in line:
                k, v = line.split("=", 1)
                os.environ.setdefault(k.strip(), v.strip())

_load_env()

DEFAULT_HOST = os.environ.get("AGENTHOST_HOST", "root@8.219.191.172")
REMOTE_DIR   = os.environ.get("AGENTHOST_DIR",  "/opt/agenthost")
RELEASE_URL  = "https://github.com/FastR-D/AgentHost/releases/latest/download/agenthost-linux-amd64.tar.gz"


def _conn(c):
    """Accept either a Connection or a host string."""
    if isinstance(c, str):
        return Connection(c)
    return c


@task(default=True)
def deploy(c, host=DEFAULT_HOST):
    """Download the latest release and restart AgentHost."""
    c = _conn(host)

    print(f"==> deploying to {host}  dir={REMOTE_DIR}")

    # 1. ensure install dir exists
    c.run(f"mkdir -p {REMOTE_DIR}", hide=True)

    # 2. download & extract (strip top-level dir from tarball)
    c.run(
        f"curl -fsSL {RELEASE_URL} | tar -xz --strip-components=1 -C {REMOTE_DIR}",
        echo=True,
    )

    # 3. make sure start.sh is executable
    c.run(f"chmod +x {REMOTE_DIR}/start.sh {REMOTE_DIR}/bin/agenthost", hide=True)

    # 4. (re)start
    with c.cd(REMOTE_DIR):
        c.run("./start.sh stop 2>/dev/null || true", hide=True)
        result = c.run("./start.sh start", echo=True)

    print("==> done")
    return result


@task
def stop(c, host=DEFAULT_HOST):
    """Stop the running AgentHost process."""
    c = _conn(host)
    with c.cd(REMOTE_DIR):
        c.run("./start.sh stop", echo=True)


@task
def logs(c, host=DEFAULT_HOST):
    """Tail the remote log."""
    c = _conn(host)
    c.run(f"tail -f {REMOTE_DIR}/agenthost.log", pty=True)


@task
def status(c, host=DEFAULT_HOST):
    """Show running status and last log lines."""
    c = _conn(host)
    with c.cd(REMOTE_DIR):
        c.run("./start.sh status", echo=True)
    c.run(f"tail -20 {REMOTE_DIR}/agenthost.log", echo=True)
