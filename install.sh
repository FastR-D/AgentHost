#!/usr/bin/env bash
# 下载并安装/升级 AgentHost
set -euo pipefail

INSTALL_DIR="${1:-/work/agenthost-linux-amd64}"
TMP=$(mktemp -d)
trap "rm -rf $TMP" EXIT

echo "==> 下载最新 release..."
curl -fsSL https://github.com/FastR-D/AgentHost/releases/latest/download/agenthost-linux-amd64.tar.gz \
  -o "$TMP/ah.tar.gz"

echo "==> 解压..."
tar -xzf "$TMP/ah.tar.gz" --strip-components=1 -C "$TMP"

echo "==> 安装到 $INSTALL_DIR ..."
mkdir -p "$INSTALL_DIR"

# 停止旧进程
if [[ -f "$INSTALL_DIR/agenthost.pid" ]]; then
  PID=$(cat "$INSTALL_DIR/agenthost.pid")
  kill "$PID" 2>/dev/null && echo "==> 旧进程已停止 (pid $PID)" || true
  rm -f "$INSTALL_DIR/agenthost.pid"
  sleep 1
fi

# 复制文件（保留已有的 .env / data/ 目录不覆盖）
rsync -a --exclude='.env' --exclude='data/' --exclude='node-data/' \
  --exclude='workspace/' --exclude='agenthost.log' --exclude='agenthost.pid' \
  "$TMP/" "$INSTALL_DIR/" 2>/dev/null \
|| cp -r "$TMP"/. "$INSTALL_DIR/"

chmod +x "$INSTALL_DIR/bin/agenthost" "$INSTALL_DIR/run.sh" 2>/dev/null || true

echo "==> 完成！启动方式："
echo "    cd $INSTALL_DIR && ./run.sh start"
