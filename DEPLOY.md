# AgentHost 部署指南

AgentHost 分两个角色：

- **Web 控制台**（公网）：浏览器访问，管理会话、任务、运行记录。
- **节点 node**（内网）：实际跑 Codex 任务的机器。它**主动往外拨号**连接 Web 控制台，所以不需要公网 IP、不用在路由器上开端口、也不用内网穿透。

换句话说，**"内网穿透"已经内建**了：只要 Web 控制台在公网（任何云服务器、VPS、Cloudflare Tunnel、frps 都行），内网机器用 node 角色连过去就行。

```
┌─────────────┐         WebSocket (出站)        ┌─────────────┐
│  公网 Web    │ ◀───────────────────────────── │  内网 node   │
│  控制台      │   node 主动拨号，可穿 NAT/防火墙  │  (跑 Codex)  │
│  :8080      │                                  │  不开端口     │
└─────────────┘                                  └─────────────┘
   浏览器访问                                          你的代码仓库
```

## 一、在公网机器部署 Web 控制台

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

从 GitHub Release 下载 `agenthost-linux-amd64.tar.gz`，解压后：

```bash
./agenthost
# 默认监听 :8080，token 自动生成并打印在日志里
# 用环境变量改地址 / 鉴权：
#   AGENTHOST_ADDR=:8080  AGENTHOST_TOKEN=your-token  ./agenthost
```

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

节点只往外连，不监听任何端口，可放在 NAT、公司防火墙、家里的宽带后面。

### 方式 A：Docker（推荐）

```bash
git clone https://github.com/FastR-D/AgentHost.git
cd AgentHost

# 编辑 docker-compose.node.yml，填入公网 Web 地址和节点令牌
#   AGENTHOST_HUB=https://agenthost.example.com
#   AGENTHOST_NODE_TOKEN=<第 4 步拿到的令牌>

docker compose -f docker-compose.node.yml up -d
docker compose -f docker-compose.node.yml logs -f
# 看到「已连接 Web 服务器」就成功了
```

### 方式 B：编译好的二进制

```bash
# 解压 release 包后：
export AGENTHOST_HUB=https://agenthost.example.com
export AGENTHOST_NODE_TOKEN=<节点令牌>
./agenthost node
```

或者用包里的启动脚本：

```bash
./start.sh node --hub https://agenthost.example.com --token <节点令牌>
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
| `AGENTHOST_SANDBOX` | `workspace-write` | 沙箱：`read-only`/`workspace-write`/`danger-full-access` |

## 常见问题

**Q：节点连不上 Web 控制台？**
看节点日志。常见原因：地址写成了 `http://` 但实际是 HTTPS（或反过来）、令牌不对、Web 控制台没开放 WebSocket。节点会自动重连，日志里会一直显示「与 Web 服务器的连接断开：…；Ns 后重连」。

**Q：一台 Web 控制台能管几个节点？**
任意多个。每个节点独立工作，会话/任务绑定到具体节点。

**Q：node 模式需要开放端口吗？**
不需要。它只发起出站 WebSocket 连接，可穿透 NAT 和大多数防火墙。

**Q：Web 控制台本身能跑任务吗？**
默认会启动一个「本机」节点。设 `AGENTHOST_LOCAL_NODE=off` 关掉它，只让远程节点干活。
