# AgentHost 交接文档（2026-11-07 更新）

> 第 A 部分是 2026-11-07 的工作；第 B 部分是 2026-11-05 的原交接文档，原样保留——其中关于 workflow、技能注入、顶部任务条 / ProcessSheet 的描述已过时，以 A 部分为准。

## A. 部署 / CI / node 连接（2026-11-07）

#### 当前状态

- **Git**: master 分支，tag `v0.1.0`
- **服务器**: `8.219.191.172`，agenthost 跑在 `/work/agenthost-linux-amd64`，`0.0.0.0:8080`，Caddy 在 80/443 反代
- **域名**: `labs.fastrd.io`，有 Let's Encrypt 证书，HTTPS/WSS 正常
- **登录 token / 节点 token**: 不写进仓库；`cd deploy && fab token` / `fab node-token` 查看（服务器上的 `data/token`、`data/node-token`）
- **部署**: `cd deploy && fab deploy`（Docker 交叉编译 → 上传 → 停旧进程 → 加载 `.env` 启动 → 8080 健康检查）

### 任务 = 一份 Markdown，去掉 workflow 编排和技能注入

- 任务就是 Markdown，原样交给一个 Codex 去做；怎么做、用哪些说明文件都写在 md 里，Codex 自己去读。
- 删掉了：`internal/workflow`（步骤/依赖/条件/变量/重试/子 workflow）、`engine/shell.go`、`engine/library.go`（workflow 库）、`engine/skills.go`（扫描并通过 `skills/extraRoots/set` 注入 SKILL.md、`$名字`）、`agenthost check`、`/api/workflows`、`/api/skills`、`/sessions/{id}/workflow`、`AGENTHOST_WORKFLOWS`、旧 workflows / skills-lib 目录迁移；前端 `StepCard`/`Pipeline`/workflow 消息、任务库和文件页的“技能”区块。
- 一个 Run 固定只有一个 Step（`codexStep()`），`scheduler.go` 只负责：等槽位 → 跑这个 Codex → 收尾。`runCtl.step` 取代按步骤的控制表。暂停/继续/停止/重跑、管家、并行子 Codex、`report_progress` 都保留。
- 旧记录里 kind=`skills` 的条目前端不显示；`#/skills` 链接仍跳到任务库。

### 长时间任务（简单版）

- 实测：Codex 用 `nohup … &` 启动的程序和 Codex 在同一进程组，回合结束就被杀；`setsid nohup … &` 能一直跑（不受回合结束、控制台重启/部署、node 断线影响）。`progressGuide` 里要求 Codex 长程序一律用 setsid 放后台、写日志和 pid 文件、然后结束本轮。
- 新状态 `interrupted`（已中断）：控制台重启（启动时把进行中的标为中断；正常关闭时 `StopAll` 设 `rc.shutdown`）、node 断线、Codex 进程意外退出（`errInterrupted`）。不再标为失败。
- Codex 线程 id 在 `thread/start` 后立刻写进会话（以前要等回合结束），中途中断也能接上。
- `Engine.Continue` / `POST /api/runs/{id}/continue`：在会话线程上开一轮“先检查状态再接着做”。卡片、执行过程里有“继续”按钮。
- 置顶卡片 `TaskBar` 回来了：状态、当前动作、进度、暂停/停止/继续/重跑；对话里的“执行过程”只负责展开。
- 验收（2026-11-07）：本地真实 Codex——后台作业撑过回合结束和控制台重启；重启后问进展用原线程；回合中途重启控制台→已中断→继续用原线程完成。线上 labs.fastrd.io + server-2——回合中途杀掉 node→已中断，作业照跑；node 重连后继续用原线程完成。
- 还没做：`agenthost job` 托管命令、定时汇报、端口代理、推送通知、断线时保住正在运行的 Codex 进程。

### 执行过程：在对话里就地展开

去掉弹出的执行过程面板（置顶卡片后来又加回来了，见上）。对话里每个任务一行（状态、当前动作、计时、暂停/停止、多步时的进度条），点一下就地展开时间线，底部有重新运行和日志；最终回答不在时间线里重复。

### 沙箱：Codex 永远不用沙箱

**现象**: codex 执行命令时报 `bwrap: No permissions to create a new namespace`，所有命令 exit 1。devcontainer 不支持 user namespaces。

**之前的修复无效**: 沙箱模式是 **hub** 建任务时定的（写进 `Run.Sandbox` 发给 node），node 上设 `AGENTHOST_SANDBOX` 不起作用；管家写死 `read-only`、“保存并测试”用 `--sandbox read-only`，也都要 bwrap。

**现在**: Codex 永远在 devcontainer 里跑，容器就是隔离边界。`settings.go` 里常量 `CodexSandbox="danger-full-access"`、`CodexApproval="never"`：
- 每次启动都带 `-c sandbox_mode="danger-full-access" -c approval_policy="never"`，`thread/start|resume` 也固定用这两个值；普通任务、管家、“保存并测试”都一样。
- 删掉了 `AGENTHOST_SANDBOX` / `AGENTHOST_APPROVAL_POLICY`、`network_access`/`writable_roots` 参数、设置页“联网”开关（后端 `NetworkAccess` 字段保留但不再使用）。
- 只需更新 **hub** 的 binary 即可生效（node 只是照着执行）。

### 已完成的工作（本次 session）

### 基础设施
- **Dockerfile**: ubuntu:24.04，3 阶段构建（web→go 交叉编译→runtime），去掉 bubblewrap
- **entrypoint.sh**: 根据 `AGENTHOST_HUB` 自动切 web/node 模式
- **build.sh**: 本地用 Docker 交叉编译 linux/amd64 binary
- **deploy/fabfile.py**: `fab deploy/stop/logs/status/token/node-token`
- **Caddy**: 服务器上已安装，配置在 `/etc/caddy/Caddyfile`，80/443 都反代到 8080

### 功能
- **node 重连 bug 修复**: `HandleConnect` mutex 无歧义，同名 node 重连 kick 旧连接
- **node 连接走 WSS**: 运营商对 80 端口 WebSocket 帧有透明代理，改用 443（WSS）后正常
- **并行"不限"选项**: `parallelUnlimited = -1`，前端显示"不限，越多越好"
- **首页无 node 提示**: `AGENTHOST_LOCAL_NODE=off` 且无在线 node 时显示引导
- **版本号**: binary 里注入 git commit + build date，`/api/info` 返回 `0.1.0-abc1234`
- **PWA**: 加 192/512 PNG icon，Edge/手机可安装
- **登录框图标对齐**: 用 flexbox 消除 token 图标与文字重叠

### 性能
- **JS chunk 拆分**: 主 chunk 862KB→164KB（gzip 257→46KB），二次访问命中缓存

### GitHub CI
- `ci.yml`: test + build linux/amd64 + docker smoke test
- `release.yml`: 打 tag 发 Release，tarball 包含 binary+start.sh+compose+DEPLOY.md
- `deploy.yml`: release 发布后自动 SSH 部署（需配 `DEPLOY_SSH_KEY` / `DEPLOY_HOST` secrets）

### 待完成

1. `internal/node` 的 `TestConnectRejections` 还按旧的“拒绝同名重连”写的，现在的行为是踢掉旧连接，测试需要改
2. `build.sh` 没把 commit 传进构建，线上版本号显示 `0.1.0-dev`
3. 80 端口 WebSocket 有透明代理问题，node 永远走 WSS（`--hub https://labs.fastrd.io`）

### 架构说明

```
用户浏览器
    ↓ HTTPS/WSS
labs.fastrd.io（Caddy 443/80 → agenthost:8080）
    ↑ WSS（wss://labs.fastrd.io）
devcontainer（agenthost node + codex + 代码）
    - AGENTHOST_SANDBOX=danger-full-access
    - 容器即隔离边界，不需要 bubblewrap
    - 一台宿主机可跑多个容器，各自连同一个 web 控制台
```

### 文件位置

| 路径 | 说明 |
|---|---|
| `deploy/Dockerfile` | 容器镜像 |
| `deploy/fabfile.py` | Fabric 部署脚本 |
| `deploy/.fab.env` | 部署配置（gitignored） |
| `build.sh` | 本地 x86-64 交叉编译 |
| `start.sh` | 启动脚本（web/node/start/stop/logs） |
| `docker-compose.yml` | web 控制台 |
| `docker-compose.node.yml` | devcontainer node |
| `DEPLOY.md` | 部署指南 |

### 服务器操作

```bash
# 部署新版
cd /work/agenthost-linux-amd64
kill $(cat agenthost.pid); sleep 2
# 上传新 binary（需先 kill 进程）
AGENTHOST_ADDR=0.0.0.0:8080 AGENTHOST_LOCAL_NODE=off \
  nohup ./bin/agenthost >> agenthost.log 2>&1 &
echo $! > agenthost.pid

# 查看日志
tail -f /work/agenthost-linux-amd64/agenthost.log

# Caddy 配置
cat /etc/caddy/Caddyfile
systemctl reload caddy
```


---

## B. 原交接文档（2026-11-05）

## 1. 最近完成：任务 + 按任务注入 skill（2026-11-05，已部署到线上实例）

`REQUIREMENTS.md` 第 5 条已实现，模型是：

- **任务** = 一份 Markdown 描述 + 在哪跑（服务器 + 目录）。可以存在任务库（`data/tasks/<名字>.md`，frontmatter 可选 `名称`/`服务器`/`目录`，也认 `title`/`server`/`cwd`），也可以是仓库里的任意 `.md`（文件浏览器里点“作为任务运行”）。
- **Skill** = 代码里自带的 `SKILL.md`。每次启动 Codex 进程时（`agentTurn` → `injectSkills`）在会话目录所在的整个 git 仓库里找（不在仓库里就只找该目录，并跳过下面嵌套仓库里的），跳过依赖和构建目录，然后把**每个技能自己的文件夹**作为 `skills/extraRoots/set` 交给这个进程。Codex 原生会加载的（先用 `skills/list` 查出来）不重复交。任务文本里写 `$名字` 会附加 `{type:"skill"}` 输入项。时间线出现一条 kind=`skills` 的条目。
- 已删除：`SkillLib`、`data/skills-lib` 同步到节点的逻辑、`/api/skills` 的增删改、`skills/` 示例目录（改为 `tasks/` 下 3 个示例任务）、`node.Info.SkillsDir`。旧的 `data/skills-lib`、`data/workflows` 保留不删；首次打开任务库时把其中用户自己的文件迁移成任务（测试workflow 已迁移）。
- 实测（真实 Codex，手机尺寸 Playwright）：仓库 `docs/x.md` 作为任务运行，Codex 自动用上 `tools/skills/…` 里的技能；`$名字` 生效；`node_modules` 里的技能没有被注入；`~/.codex/skills` 不变。

- **当前服务器**（2026-11-05 第二轮）：侧边栏顶部和手机顶栏标题下方是“当前服务器”切换（`ServerSwitcher.tsx`，存 localStorage `agenthost.node`，`useCurrentNode()`）。会话列表、运行记录、首页、任务库、文件、目录选择都只针对当前服务器；目录弹窗不再选服务器。打开别的服务器的会话/文件链接会自动切过去。任务库按服务器分开：新任务的 `node` = 当前服务器；没有 `node` 的（示例、迁移来的）在每台服务器上都显示为“所有服务器通用”。
- 技能扫描不看 AgentHost 自己的数据目录；会话目录在工作区内而 git 根在工作区之上时（比如工作区在 AgentHost 仓库里），只扫会话目录。时间线上的技能条目默认折叠成一行。

- **管家 + 一条对话**（2026-11-06）：
  - 会话 = 一条对话。顶部 `TaskBar`（`web/src/components/TaskBar.tsx`）显示最近任务的状态、正在做什么、进度和暂停/继续/停止/重跑；点它打开 `ProcessSheet`（完整执行过程）。对话里每个任务只留一行 `ProcessLine`，加上最终回答（`messages.ts` 的 `buildMessages`）。如果任务运行期间有管家问答，结果会排到问答后面，前面加“✓ 任务完成”。
  - 任务运行中，`POST /sessions/{id}/chat` 发给管家（`internal/engine/manager.go`）：`Run.Kind = "manager"`，`Target` 指向任务 run，sandbox read-only，不占执行槽位，用会话里单独的 `ManagerThreadID`。每次的 prompt 都带 `taskSnapshot`（步骤、最近 15 个动作、命令输出末尾）。管家的动态工具有 `get_task_status`、`send_to_task`（→ `Engine.Steer` → `turn/steer`，任务时间线里加一条“管家转告”）、`pause_task`/`resume_task`/`stop_task`。`sessionBusy`、`sessionInfo`、运行记录、侧边栏计数都忽略 manager run。
  - 真实验证过：问进度答得准，工作中的 Codex 不受影响；转告“第三条不要做，改写 d.txt”，Codex 当场改了做法。
- Markdown：`fixCjkEmphasis`（`Markdown.tsx`）把 `**问题：**后文` 改写成 `**问题**：后文`，否则 CommonMark 不认这种粗体。
- README 带截图（`docs/images/*.png`，iPhone 13 尺寸、2 倍图，外加一张电脑端）。重拍的脚本在 `/tmp/pw-scripts/readme.js`、`readme2.js`（会被清掉的话照着 README 里的场景重搭：示例仓库 shop-api、release-notes 技能、gpu-1 节点）。

- **运行记录管理**（2026-11-06）：`internal/engine/runs.go`。`RunStats`、`DeleteRun`（只删已结束的；连同子 workflow、指向它的管家回答、日志一起删；会话空了就删会话）、`CleanupRuns(node, olderThan, dryRun)`、`retentionLoop`（设置 `runs.retentionDays`，每小时一次）。`ListRuns` 支持 `node`/`before`/`tasks`。前端 `RunsView` 自己按页拉取（每页 30），用 store 里的实时版本覆盖；store 的 `resync` 只加载进行中的和最近 40 条。`run.deleted` 事件会放进 `deletedRuns`。
- **一个任务多个 Codex**（2026-11-06）：`internal/engine/parallel.go`。`Run.Parallel`（2–8，0 表示一个），来自请求、任务库（frontmatter `并行`），或者沿用会话里上一个任务。启动时加 `-c features.multi_agent=true -c agents.max_concurrent_threads_per_session=N`，第一轮多发一段 `parallelHint`。Codex 子 agent 的事件会带着各自的 threadId 推给同一个客户端（实测：3 个子 agent 的命令时间段重叠）；主线程的 `subAgentActivity{kind: started|completed, agentThreadId, agentPath}` 用来维护 `Step.Agents`（`SubAgent{name, status, activity, tokens}`），子线程的条目都带 `Item.ThreadID`。前端 `TaskBar.tsx` 的 `Timeline`/`SubAgentCard` 按子 Codex 分组显示，`ParallelChip`（`Parallel.tsx`）用来选数量。实测上限 N 只算子 agent：设 2、要求 3 个时，第三个会等前两个结束才开始。

可以继续做的：
- 任务库里的任务只能在“任务库/常用任务/输入框旁的任务按钮”运行，暂时没有定时运行。
- 后端旧 workflow（`internal/workflow`、`StartWorkflow`、`/api/workflows`、`library.go`）仍保留，只为兼容旧运行记录，可以继续瘦身。

## 2. 项目概况

- 位置：`/work/AgentHost`（实际是 Linux arm64 容器，`HOME=/root`）。
- Git：`master` 分支只有一个 `init` 提交，**所有工作都未提交**；用户没让提交。
- 后端 Go 单二进制（内嵌前端）；前端 React + assistant-ui + Tailwind + zustand；中文界面，手机优先。
- 通过 `codex app-server`（stdio JSON-RPC）驱动 Codex CLI 0.166。

### 已完成并实测过的功能
- 聊天式任务：实时显示思考、命令（cwd/输出/退出码）、文件 diff、计划；暂停/继续/停止/重跑；同一会话追问续接 Codex thread。
- 每个 Codex 线程都注册了动态工具 `report_progress`，并通过 developerInstructions 提示使用；界面显示“步骤 2/4：…”和进度。
- 多服务器：`agenthost node --hub URL --token T --name N` 主动通过 WebSocket 连回控制台（可在 NAT 后面）；会话绑定服务器 + 目录；断线时把任务标记为失败并结束进程；`AGENTHOST_LOCAL_NODE=off` 可让控制台本机不跑任务。
- 设置页：Codex Base URL / API Key / 模型 / 推理强度 / 联网；Git SSH 密钥（可生成 ed25519，显示公钥）、HTTPS 令牌（可限定主机）、提交者信息；“保存并测试”按钮可用。
- 文件浏览（只读，任意服务器）、Git 状态条与“同步到 Git”（Codex 提交并推送；仅为该任务放开联网和该仓库 `.git` 写权限）。
- 手机适配：抽屉菜单、底部弹出面板、安全区、iOS 输入框不缩放、PWA manifest。
- `start.sh`：自动安装 Go/Node/Codex，按源码哈希跳过构建；`./start.sh node …` 以节点角色运行；附带 Dockerfile 与 compose 文件。
- 数据目录加锁（防止两个实例共用）；端口被占用时给出明确提示。

### 已放弃 / 遗留
- 结构化 workflow（`## 1. 标题` + 依赖/条件等设置、编辑器、预览、整理 workflow 功能）已按用户要求**从界面移除**。后端 `internal/workflow`、`StartWorkflow`、`/api/workflows`、`library.go` 仍保留，仅用于兼容旧的运行记录，可以继续瘦身。
- 旧“技能库”已改造为任务库（见第 1 节）。

## 3. 代码地图

后端：
- `cmd/agenthost/`：`main.go`（环境变量与启动、注册表、关闭流程）、`instance.go`（数据目录锁、端口报错）、`nodecmd.go`（`node` 子命令）、`check.go`（旧 workflow 检查）。
- `assets.go`：内嵌 `web/dist` 和 `tasks/`（3 个示例任务）。
- `internal/codex/client.go`：app-server 客户端，通过 `node.Proc` 启动，`Options{Node,Bin,ExtraArgs,Env,Cwd,Experimental}`。
- `internal/node/`：
  - `Node` 接口：`Info`、`Start`、`ListDir`、`MkdirAll`、`IsDir`、`WriteFile`、`ReadDir`、`ReadFile`。
  - `local.go` 本机执行；`remote.go` 控制台侧，消息结构为 `msg`；`agent.go` 节点侧；`registry.go` 处理 `/api/nodes/connect`，持久化 `nodes.json`，提供 `Close()`。
- `internal/engine/`：
  - `engine.go`：会话、运行、`startChat(req, title, network, writable...)`、重试、控制。
  - `scheduler.go`：调度。
  - `agent.go`：`runAgent`/`agentTurn`、`progressTool`、`onToolCall`、审批。
  - `shell.go`：shell 步骤，经节点执行。
  - `settings.go`：`codexArgsFor`、`extraEnv`、SSH 密钥、TestGit/TestCodex、`runCheck`。
  - `files.go`：文件浏览、GitStatus、GitSync。
  - `tasks.go`：`TaskLib`（任务库 CRUD、示例、从旧目录迁移）。
  - `skills.go`：`DiscoverSkills`/`discoverSkills`（在节点上跑 find 脚本）、`injectSkills`、`mentionedSkills`、`skillsNotice`。
  - `codexitems.go`：ThreadItem 到 Item 的转换；`hiddenTools` 隐藏 `report_progress`。
  - `model.go`、`store.go`（JSON 加 JSONL 日志）、`hub.go`（SSE 广播）。
- `internal/server/server.go`：全部 HTTP 路由、认证（cookie 或 Bearer）、SSE `/api/events`。

前端 `web/src/`：
- 基础：`App.tsx`（路由、手机顶栏和抽屉）、`store.ts`（zustand、SSE、路由解析、`nodeName`）、`api.ts`、`types.ts`。
- `components/`：
  - 页面：`HomeView`、`ChatView`（含 `SessionHeader`）、`RunsView`（运行记录，`#/runs`）、`TaskLibView`（任务库，`#/tasks`；`#/skills` 兼容跳转）、`FilesView`（含 `RunFileSheet`）、`ServersView`、`SettingsView`。
  - 对话：`Thread`（assistant-ui 原语；`Composer`；`TaskRequestPart`）、`messages.ts`（运行记录转消息）、`StepCard`（`RunControls`、`Pipeline`、`progressUnits`、`ActivityLine`、`ResultBlock`）、`ItemView`（含 `SkillsRow`）。
  - 公共：`DirPicker`（`Sheet`/`DirPicker`/`WhereChip`）、`GitBar`、`TaskSheet`（`useTasks`、`RepoSkills`、`runTaskInSession`）、`LogsPanel`、`Sidebar`、`Markdown`。

## 4. 运行状态

- 线上实例：见 `ps -eo pid,args | grep bin/agenthost`（`bin/agenthost`，经 `setsid nohup ./start.sh` 启动），端口 `:8080`，日志在 `/tmp/agenthost-live.log`。
- 访问地址：`http://localhost:8080/?token=<data/token 的内容>`。登录令牌在 `data/token`，节点令牌在 `data/node-token`（不要写进仓库里的文件）。
- `data/tasks/` 是任务库（3 个示例 + 迁移来的 `测试workflow`）；`data/workflows/`、`data/skills-lib/` 是旧目录，保留未删。
- 测试用实例（`/tmp/ah-*`）已全部停掉。

## 5. 测试与构建

```bash
cd /work/AgentHost
CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./...      # 目前全部通过（engine/node/server/workflow）
cd web && npx tsc --noEmit -p tsconfig.json && npx vite build && touch dist/.gitkeep
cd .. && ./start.sh --install-only                              # 构建 bin/agenthost
```

- engine 测试把测试二进制本身当作假 `codex app-server`（环境变量 `FAKE_CODEX=1`，见 `engine_test.go` 的 `fakeAppServer`）。它支持 thread/turn/interrupt、`item/tool/call` 回调、`skills/extraRoots/set`、`skills/list`（`fakeSkills`）；回答末尾写 `[roots <各 root 的文件夹名>]` 和每个 skill 输入项 `[skill <名>]`。任务和技能的测试在 `tasks_test.go`。
- server 测试的 engine 用 `CodexBin: "/bin/false"`，避免调起真实 Codex。
- 远程节点测试：`TestRemoteServer`（engine 包）和 `node_test.go`（真实 WebSocket）。

## 6. 环境坑

- Go 1.21.6 是 amd64 版本，运行在 arm64 上：编译用 `CGO_ENABLED=0 GOARCH=arm64`，测试用 `CGO_ENABLED=0`（不能开 `-race`）。不能用 Go 1.22 的 API，例如 `http.ServeFileFS`。
- Node 18：nanoid 的 EBADENGINE 警告可以忽略。npm 镜像很慢，npm 偶尔崩溃（"Exit handler never called"），安装时加 `--prefer-offline`（缓存已经热了）。
- **不要用 `pkill -f`**：会杀掉自己所在的 shell（exit 144）。改用 `ps -eo pid,args | awk …` 找 pid 再 kill。
- 没有 `/usr/bin/time`，也没有 PIL。
- Playwright 和 chromium 在 `/tmp/pw/node_modules/playwright`，测试脚本在 `/tmp/pw-scripts/`：
  - 手机尺寸用 `devices['iPhone 13']`。
  - 页面同时渲染桌面侧栏和手机抽屉，点击时用 `button:visible` 或限定在 `.fixed.inset-0` 内。
  - `tile.js` 可以把三张截图拼成一张，方便查看。
  - `/tmp` 可能被清空。
- 本机 Codex 配置 `~/.codex/config.toml` 使用自定义 provider（gpt.fastrd.io，模型 gpt-6-sol）。会报 2 个“未识别配置项”的警告，可忽略。
- 上一轮末尾 Bash 的安全分类器暂时不可用，命令被拒；重试即可。

## 7. 已验证的 Codex app-server 事实（0.166）

- 握手：`initialize{clientInfo, capabilities:{experimentalApi:true}}`，然后发送 `initialized` 通知。`dynamicTools` 需要 experimentalApi。
- 线程与回合：
  - `thread/start{cwd,sandbox,approvalPolicy,model,dynamicTools,developerInstructions,ephemeral}`，`thread/resume{threadId,…}`。
  - 换一个新进程 resume 后，动态工具仍然可用（已验证）。
  - `turn/start{threadId, input:[{type:"text",text,text_elements:[]},{type:"skill",name,path}], summary}`；`turn/interrupt`。
- 动态工具调用：服务端请求 `item/tool/call`，回复 `{success, contentItems:[{type:"inputText",text}]}`。
- Skill：
  - `skills/extraRoots/set{extraRoots:[...]}` 只对当前进程生效（另起进程看不到），不写 `config.toml`、不写 `~/.codex/skills`。每个 root 会被**递归**扫描（包括 `node_modules`，但跳过隐藏目录）；root 本身就是技能文件夹（root/SKILL.md）也可以；显式给出的隐藏目录可以；跟随符号链接（报告真实路径）；重叠的 root 按路径去重；不存在的 root 不报错。所以 AgentHost 传的是每个技能自己的文件夹。
  - Codex 原生加载 `.agents/skills`、`.codex/skills`（从 cwd 往上到 git 根），scope=`repo`。
  - `skills/list{cwds,forceReload}` → `data[].skills[{name,description,path,scope,enabled}]`、`data[].errors`，很快（几毫秒）。
  - 请求和 skill 描述匹配时 Codex 会自动使用（已验证）；`{type:"skill",name,path}` 输入项强制使用。
- `turn/steer{threadId, expectedTurnId, input:[UserInput]}` → `{turnId}`：往进行中的回合追加用户输入（已实测可用）（schema 用 `codex app-server generate-json-schema --out DIR` 导出）。
- 注意：Codex 在 git 仓库里 `thread/start` 时会自己往 `~/.codex/config.toml` 写 `[projects."<仓库>"] trust_level = "trusted"`，与 skill 无关，用 `-c` 覆盖也挡不住。测试后要清理的话，删掉对应的段即可。
- 沙箱：`workspace-write` 不能写 `.git`，用 `-c sandbox_workspace_write.writable_roots=["…/.git"]`；联网用 `-c sandbox_workspace_write.network_access=true`。
- Provider 覆盖：`-c model_provider="agenthost" -c 'model_providers.agenthost={name=…, base_url=…, wire_api=…, env_key="AGENTHOST_CODEX_API_KEY", requires_openai_auth=false}'`。
- Codex 执行的命令看不到名字里带 KEY/TOKEN/SECRET 的环境变量，所以 Git 密码用 `AGENTHOST_GIT_PASS`。
- `codex exec` 在 stdin 未关闭时会一直等待，所以 `runCheck` 会先关闭 stdin。
- 命令输出的第一段不会以增量推送，短命令要等结束后才显示输出。

## 8. 用户偏好

- 用中文交流；主要在手机上用；要的是纯文字任务，不要结构化的 workflow 语法或预览；所有配置都在网页里完成，不登录服务器操作。
- skill 来自代码，按任务注入，**绝不全局安装**。
- 用户会在工作过程中插话改需求；动手前先确认理解对了。重要结论要在真实浏览器（手机尺寸）里验证后再汇报。
- 长期记忆已写在 `/root/.claude/projects/-work/memory/`。
