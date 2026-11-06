export type Status =
  | "queued"
  | "pending"
  | "running"
  | "paused"
  | "completed"
  | "failed"
  | "stopped"
  | "skipped";

export interface Session {
  id: string;
  title: string;
  cwd: string;
  nodeId: string;
  threadId?: string;
  managerThreadId?: string;
  runIds: string[];
  createdAt: string;
  updatedAt: string;
  status?: Status | "";
  activity?: string;
  lastRunId?: string;
}

export interface FileChange {
  path: string;
  kind: string;
  diff?: string;
}

export interface PlanEntry {
  step: string;
  status: string;
  note?: string;
}

export type ItemKind =
  | "message"
  | "reasoning"
  | "command"
  | "file_change"
  | "tool"
  | "web_search"
  | "plan"
  | "agent"
  | "error"
  | "notice"
  | "skills"
  | "other";

export interface Item {
  id: string;
  kind: ItemKind;
  status?: string;
  title?: string;
  phase?: string;
  text?: string;
  command?: string;
  argv?: string[];
  cwd?: string;
  output?: string;
  stdout?: string;
  stderr?: string;
  exitCode?: number | null;
  durationMs?: number;
  changes?: FileChange[];
  plan?: PlanEntry[];
  server?: string;
  tool?: string;
  arguments?: unknown;
  result?: string;
  threadId?: string;
  /** Sub-agent events: the sub-agent's thread. */
  agentThread?: string;
  raw?: unknown;
  startedAt?: string;
  endedAt?: string;
}

export interface Process {
  node?: string;
  argv: string[];
  cwd: string;
  pid: number;
  startedAt: string;
  endedAt?: string;
  exitCode?: number | null;
  error?: string;
}

export interface Step {
  id: string;
  title: string;
  type: "agent" | "shell" | "workflow";
  dependsOn: string[];
  if: string;
  continueOnError?: boolean;
  status: Status;
  attempt: number;
  maxAttempts: number;
  cwd: string;
  prompt?: string;
  run?: string;
  workflow?: string;
  childRunId?: string;
  threadId?: string;
  turnId?: string;
  processes?: Process[];
  items?: Item[];
  itemCount?: number;
  output?: string;
  error?: string;
  exitCode?: number | null;
  diff?: string;
  plan?: PlanEntry[];
  orchestrate?: boolean;
  usage?: TokenUsage;
  /** Codex sub-agents working on parts of this step. */
  agents?: SubAgent[];
  activity?: string;
  startedAt?: string;
  endedAt?: string;
}

export interface SubAgent {
  threadId: string;
  name: string;
  status: "running" | "completed" | "stopped" | string;
  activity?: string;
  tokens?: number;
  startedAt?: string;
  endedAt?: string;
}

export interface TokenUsage {
  total?: { totalTokens?: number; inputTokens?: number; outputTokens?: number; cachedInputTokens?: number; reasoningOutputTokens?: number };
  last?: { totalTokens?: number };
  modelContextWindow?: number | null;
}

export interface WorkflowDef {
  name: string;
  description?: string;
  instructions?: string;
  cwd?: string;
  model?: string;
  sandbox?: string;
  maxParallel: number;
  vars?: Record<string, string>;
  mode: "steps" | "agent";
  modeReason?: string;
  detectedSteps: number;
  contextSections?: string[];
  steps: {
    id: string;
    number: number;
    title: string;
    type: string;
    dependsOn: string[];
    autoDeps?: boolean;
    parallel?: boolean;
    orchestrate?: boolean;
    if: string;
    retry?: number;
    timeout?: number;
    continueOnError?: boolean;
    cwd?: string;
    model?: string;
    sandbox?: string;
    session?: string;
    prompt?: string;
    run?: string;
    workflow?: string;
    with?: Record<string, string>;
    line: number;
  }[];
}

export interface Progress {
  done: number;
  total: number;
  running: number;
  failed: number;
}

export interface Run {
  id: string;
  sessionId: string;
  parentRunId?: string;
  parentStepId?: string;
  /** "manager": a reply from the task's manager while the task runs. */
  kind: "chat" | "workflow" | "manager";
  title: string;
  prompt: string;
  /** Manager replies: the task run asked about. */
  target?: string;
  /** How many Codex work on the task at once (sub-agents); absent = one. */
  parallel?: number;
  source?: string;
  workflowFile?: string;
  workflow?: WorkflowDef;
  mode?: string;
  /** Task from the library this run follows. */
  task?: string;
  /** Or the Markdown file on the server it follows. */
  taskFile?: string;
  taskTitle?: string;
  /** What the user added to the task. */
  note?: string;
  network?: boolean;
  vars?: Record<string, string>;
  cwd: string;
  nodeId: string;
  model?: string;
  sandbox?: string;
  status: Status;
  steps: Step[];
  output?: string;
  error?: string;
  activity?: string;
  createdAt: string;
  startedAt?: string;
  endedAt?: string;
  retryOf?: string;
  progress?: Progress;
}

export interface LogEntry {
  seq: number;
  time: string;
  runId: string;
  stepId?: string;
  source: "host" | "codex" | "rpc" | "exec" | string;
  level?: string;
  text: string;
}

export interface WorkflowInfo {
  name: string;
  title: string;
  description?: string;
  steps: number;
  error?: string;
  updatedAt: string;
  size: number;
}

export interface ParseResult {
  workflow?: WorkflowDef;
  layers?: string[][];
  warnings?: string[];
  error?: string;
  line?: number;
}

export interface Info {
  version: string;
  codexBin: string;
  codexVersion: string;
  workspace: string;
  dataDir: string;
  workflowsDir: string;
  defaultModel: string;
  defaultSandbox: string;
  approvalPolicy: string;
  maxRuns: number;
  authEnabled: boolean;
}

export const isActive = (s?: Status | "") => s === "queued" || s === "running" || s === "paused";
export const isTerminal = (s?: Status | "") =>
  s === "completed" || s === "failed" || s === "stopped" || s === "skipped";

export type Mode = "" | "steps" | "agent";

/** A saved task: plain Markdown, plus where it usually runs. */
export interface Task {
  name: string;
  title: string;
  node?: string;
  cwd?: string;
  parallel?: number;
  body: string;
  updatedAt: string;
}

/** A skill found in the code (a folder with SKILL.md). */
export interface RepoSkill {
  name: string;
  description?: string;
  path: string;
  /** Folder relative to the scanned root. */
  rel: string;
  /** Codex loads it by itself (.agents/skills, .codex/skills). */
  native?: boolean;
}

export interface SkillScan {
  root: string;
  skills: RepoSkill[];
  truncated?: boolean;
  error?: string;
}

export interface NodeInfo {
  id: string;
  name: string;
  local: boolean;
  online: boolean;
  hostname?: string;
  os?: string;
  arch?: string;
  version?: string;
  codexVersion?: string;
  workspace?: string;
  addr?: string;
  connectedAt?: string;
  lastSeen?: string;
  procs: number;
}

export interface FileEntry {
  name: string;
  dir: boolean;
  size: number;
  modTime: string;
  link?: boolean;
}

export interface FileList {
  node: string;
  path: string;
  parent: string;
  entries: FileEntry[];
}

export interface FileView {
  node: string;
  path: string;
  size: number;
  modTime: string;
  kind: "text" | "image" | "binary";
  mime?: string;
  text?: string;
  truncated: boolean;
}

export interface GitStatus {
  repo: boolean;
  root?: string;
  branch?: string;
  upstream?: string;
  ahead: number;
  behind: number;
  files: { path: string; status: string; staged: boolean }[];
  remote?: string;
  last?: string;
  error?: string;
}

export interface SettingsView {
  codex: {
    baseUrl: string;
    wireApi: string;
    model: string;
    reasoningEffort: string;
    networkAccess: boolean;
    apiKeySet: boolean;
    apiKeyHint?: string;
  };
  git: {
    sshPublicKey: string;
    sshKeySet: boolean;
    tokenUser: string;
    tokenHost: string;
    tokenSet: boolean;
    tokenHint?: string;
    userName: string;
    userEmail: string;
  };
  runs: { retentionDays: number };
  updatedAt: string;
}

export interface RunStats {
  total: number;
  active: number;
  counts: Record<string, number>;
}

export interface CheckResult {
  ok: boolean;
  output: string;
}
