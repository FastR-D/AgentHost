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
  | "skills" // older runs only
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

/** The Codex working on a run (a run has exactly one). */
export interface Step {
  id: string;
  title: string;
  status: Status;
  cwd: string;
  prompt?: string;
  threadId?: string;
  turnId?: string;
  processes?: Process[];
  items?: Item[];
  itemCount?: number;
  output?: string;
  error?: string;
  diff?: string;
  /** Steps Codex reported with report_progress. */
  plan?: PlanEntry[];
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

export interface Progress {
  done: number;
  total: number;
  running: number;
  failed: number;
}

export interface Run {
  id: string;
  sessionId: string;
  /** "manager": a reply from the task's manager while the task runs. */
  kind: "chat" | "manager";
  title: string;
  prompt: string;
  /** Manager replies: the task run asked about. */
  target?: string;
  /** How many Codex work on the task at once (sub-agents); absent = one. */
  parallel?: number;
  /** Task from the library this run follows. */
  task?: string;
  /** Or the Markdown file on the server it follows. */
  taskFile?: string;
  taskTitle?: string;
  /** What the user added to the task. */
  note?: string;
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

export interface Info {
  version: string;
  codexBin: string;
  codexVersion: string;
  workspace: string;
  dataDir: string;
  defaultModel: string;
  defaultSandbox: string;
  approvalPolicy: string;
  maxRuns: number;
  authEnabled: boolean;
}

export const isActive = (s?: Status | "") => s === "queued" || s === "running" || s === "paused";
export const isTerminal = (s?: Status | "") =>
  s === "completed" || s === "failed" || s === "stopped" || s === "skipped";

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
