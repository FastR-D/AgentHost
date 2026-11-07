# AgentHost 部署指南

AgentHost 分两个角色，**用不同的容器镜像**：

- **Web 控制台**（公网，`runtime-web`）：浏览器访问，管理会话、任务、运行记录。镜像里**没有** Codex CLI / Node.js / 开发工具，很小。它不跑任何任务（`AGENTHOST_LOCAL_NODE=off`）。
- **节点 node**（内网，`runtime-node`）：实际跑 Codex 任务的机器，基于 ubuntu:24.04 的全功能 dev container。所有 Codex 执行、工作目录、任务日志都在这里。它**主动往外拨号**连接 Web 控制台，所以不需要公网 IP、不用开端口、不用内网穿透。

换句话说，**"内网穿透"已经内建**了：只要 Web 控制台在公网（任何云服务器、VPS、Cloudflare Tunnel、frps 都行），内网机器用 node 角色连过去就行。

```
┌─────────────────┐         WebSocket (出站)        ┌─────────────────┐
│  公网 Web 控制台  │ ◀───────────────────────────── │  内网 node 容器   │
│  runtime-web     │   node 主动拨号，可穿 NAT/防火墙  │  runtime-node    │
│  :8080           │                                  │  Codex + 工作目录  │
│  无 Codex CLI     │                                  │  ubuntu 24.04     │
└─────────────────┘                                  └─────────────────┘
   浏览器访问                                          你的代码仓库 + 日志
```

## 一、在公网机器部署 Web 控制台

Web 控制台镜像（`runtime-web`）只有 agenthost 二进制，没有 Codex CLI，很小。

### 方式 A：用 Docker（推荐）

```bash
git clone https://github.com/FastR-D/AgentHost.git
cd AgentHost
docker compose up -d
```

打开日志拿登录地址：

```bash
docker compose logs | grep "Open:"
#  Open: http://localhost:8080/?token=xxxxxxxx
```

把 `http://localhost:8080` 换成你的公网域名/IP，比如 `https://agenthost.example.com`。建议在前面加一个 Nginx/Caddy 做 HTTPS 反代（Codex 走 WebSocket，记得允许 `Upgrade` 头）。

### 方式 B：用编译好的二进制

从 GitHub Release 下载 `agenthost-linux-amd64.tar.gz`，解压后直接运行：

```bash
tar -xzf agenthost-linux-amd64.tar.gz
cd agenthost-linux-amd64

# 复制并编辑配置（可选）
cp .env.example .env
# 编辑 .env，至少设置：
#   AGENTHOST_ADDR=:8080
#   AGENTHOST_LOCAL_NODE=off   # web 控制台模式

# 启动（会自动读取同目录的 .env）
set -a; source .env; set +a
./agenthost
```

日志里会打印登录地址和 token。

## 二、把公网地址暴露出去（任选其一）

Web 控制台需要一个公网可达的地址给内网节点和浏览器连。任选一种：

- **云服务器**：直接在 VPS 上跑，绑定公网 IP / 域名。
- **Cloudflare Tunnel**（免费、无需公网 IP）：
  ```bash
  cloudflared tunnel --url http://localhost:8080
  # 得到一个 https://*.trycloudflare.com 地址
  ```
- **frp / ngrok / tailscale serve**：把本地 8080 映射到一个公网域名即可。

## 三、在内网机器部署节点 node

节点容器（`runtime-node`）基于 ubuntu:24.04，内置 Codex CLI 和完整开发环境。它只往外连，不监听端口，可放在 NAT、公司防火墙、家里的宽带后面。

**dev container 思路**：容器本身就是开发环境，代码和工作目录全在容器内，不需要挂载宿主目录。一台机器可以起多个容器，每个都是独立的 node，用不同名字区分。

### 方式 A：Docker（推荐）

```bash
git clone https://github.com/FastR-D/AgentHost.git
cd AgentHost

# 编辑 docker-compose.node.yml，填入公网 Web 地址、节点令牌和节点名：
#   AGENTHOST_HUB=https://agenthost.example.com
#   AGENTHOST_NODE_TOKEN=<第 4 步拿到的令牌>
#   AGENTHOST_NODE_NAME=dev-1

docker compose -f docker-compose.node.yml up -d
docker compose -f docker-compose.node.yml logs -f
# 看到「已连接 Web 服务器」就成功了
```

多节点：在 `docker-compose.node.yml` 里复制 `agenthost-node` 块，改不同的 `container_name` 和 `AGENTHOST_NODE_NAME` 即可。

### 方式 B：编译好的二进制

```bash
tar -xzf agenthost-linux-amd64.tar.gz
cd agenthost-linux-amd64
AGENTHOST_HUB=https://agenthost.example.com \
AGENTHOST_NODE_TOKEN=<节点令牌> \
./agenthost node
```

## 四、在 Web 控制台拿到节点令牌

1. 浏览器打开公网 Web 控制台，用日志里的 token 登录。
2. 进入「服务器」页面，点「添加节点」（或类似入口），复制显示的**节点令牌**。
3. 把这个令牌填到内网节点的 `AGENTHOST_NODE_TOKEN` 里。

## 五、开始用

- 在首页输入要做的事，选目标服务器（你刚加的内网节点）和工作目录，点运行。
- 或在「任务库」选一个存好的任务，指定服务器和目录运行。
- 任务在内网节点的容器里跑，Codex 实时把思考、命令、文件改动推回来，手机上也能看。

## 环境变量速查

Web 控制台：

| 变量 | 默认 | 说明 |
|---|---|---|
| `AGENTHOST_ADDR` | `:8080` | 监听地址 |
| `AGENTHOST_TOKEN` | 自动生成 | 登录令牌 |
| `AGENTHOST_AUTH=none` | （开） | 关闭鉴权（仅可信网络用） |
| `AGENTHOST_DATA` | `./data` | 状态目录 |
| `AGENTHOST_WORKSPACE` | `./workspace` | 默认工作目录 |
| `AGENTHOST_MAX_RUNS` | `4` | 同时跑的任务数 |

节点 node：

| 变量 | 默认 | 说明 |
|---|---|---|
| `AGENTHOST_HUB` | 必填 | 公网 Web 控制台地址 |
| `AGENTHOST_NODE_TOKEN` | 必填 | 控制台发的节点令牌 |
| `AGENTHOST_NODE_NAME` | 主机名 | 显示在服务器列表的名字 |
| `AGENTHOST_WORKSPACE` | `./workspace` | 任务工作目录 |

## 常见问题

**Q：节点连不上 Web 控制台？**
看节点日志。常见原因：地址写成了 `http://` 但实际是 HTTPS（或反过来）、令牌不对、Web 控制台没开放 WebSocket。节点会自动重连，日志里会一直显示「与 Web 服务器的连接断开：…；Ns 后重连」。

**Q：一台 Web 控制台能管几个节点？**
任意多个。每个节点独立工作，会话/任务绑定到具体节点。

**Q：node 模式需要开放端口吗？**
不需要。它只发起出站 WebSocket 连接，可穿透 NAT 和大多数防火墙。

**Q：Web 控制台本身能跑任务吗？**
不能，也不需要。`docker-compose.yml` 里默认 `AGENTHOST_LOCAL_NODE=off`，任务全由 node 容器执行。Codex CLI 和工作日志都在 node 容器里，Web 控制台只负责 UI 和转发。
