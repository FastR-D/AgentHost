# AgentHost 交接文档（2026-11-05）

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
