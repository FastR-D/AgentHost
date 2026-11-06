import type {
  CheckResult,
  FileList,
  FileView,
  GitStatus,
  Info,
  LogEntry,
  Mode,
  NodeInfo,
  ParseResult,
  Run,
  Session,
  SettingsView,
  SkillScan,
  Task,
  WorkflowInfo,
} from "./types";

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

let onUnauthorized: (() => void) | null = null;
export const setUnauthorizedHandler = (fn: () => void) => {
  onUnauthorized = fn;
};

async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(`/api${path}`, {
    method,
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    credentials: "same-origin",
  });
  const text = await res.text();
  let data: any = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = { error: text };
  }
  if (!res.ok) {
    if (res.status === 401 && onUnauthorized) onUnauthorized();
    throw new ApiError(res.status, data?.error ?? `HTTP ${res.status}`);
  }
  return data as T;
}

export interface LaunchResult {
  session: Session;
  run: Run;
}

export const api = {
  auth: () => req<{ required: boolean; authenticated: boolean }>("GET", "/auth"),
  login: (token: string) => req<{ authenticated: boolean }>("POST", "/auth", { token }),
  logout: () => req("DELETE", "/auth"),
  info: () => req<Info>("GET", "/info"),

  sessions: () => req<Session[]>("GET", "/sessions"),
  session: (id: string) => req<{ session: Session; runs: Run[] }>("GET", `/sessions/${encodeURIComponent(id)}`),
  createSession: (title: string, cwd: string, nodeId?: string) => req<Session>("POST", "/sessions", { title, cwd, nodeId }),
  updateSession: (id: string, patch: { title?: string; cwd?: string; nodeId?: string }) =>
    req<Session>("PATCH", `/sessions/${encodeURIComponent(id)}`, patch),
  deleteSession: (id: string) => req("DELETE", `/sessions/${encodeURIComponent(id)}`),
  /** A message, or a task (with text as extra instructions). */
  chat: (sessionId: string, text: string, task?: { task?: string; taskFile?: string }) =>
    req<LaunchResult>("POST", `/sessions/${encodeURIComponent(sessionId)}/chat`, { text, ...task }),
  startWorkflow: (
    sessionId: string,
    body: { name?: string; source?: string; cwd?: string; vars?: Record<string, string>; mode?: Mode },
  ) => req<LaunchResult>("POST", `/sessions/${encodeURIComponent(sessionId)}/workflow`, body),
  /** Start a run, in a new session unless sessionId is given. */
  startTask: (body: {
    sessionId?: string;
    title?: string;
    cwd?: string;
    text?: string;
    workflow?: string;
    source?: string;
    vars?: Record<string, string>;
    mode?: Mode;
    nodeId?: string;
    /** A task from the library; a new session defaults to its server and directory. */
    task?: string;
    /** A Markdown file on the server; a new session defaults to its directory. */
    taskFile?: string;
  }) => req<LaunchResult>("POST", "/tasks", body),

  runs: (params: { status?: string; sessionId?: string; limit?: number } = {}) => {
    const q = new URLSearchParams();
    if (params.status) q.set("status", params.status);
    if (params.sessionId) q.set("sessionId", params.sessionId);
    if (params.limit) q.set("limit", String(params.limit));
    return req<Run[]>("GET", `/runs?${q}`);
  },
  run: (id: string) => req<Run>("GET", `/runs/${encodeURIComponent(id)}`),
  control: (id: string, action: "pause" | "resume" | "stop" | "retry") =>
    req<any>("POST", `/runs/${encodeURIComponent(id)}/${action}`),
  logs: (id: string, after = 0) => req<LogEntry[]>("GET", `/runs/${encodeURIComponent(id)}/logs?after=${after}`),

  workflows: () => req<{ dir: string; workflows: WorkflowInfo[] }>("GET", "/workflows"),
  workflow: (name: string) =>
    req<{ name: string; source: string; parsed: ParseResult }>("GET", `/workflows/${encodeURIComponent(name)}`),
  saveWorkflow: (name: string, source: string) =>
    req<{ name: string; parsed: ParseResult }>("PUT", `/workflows/${encodeURIComponent(name)}`, { source }),
  deleteWorkflow: (name: string) => req("DELETE", `/workflows/${encodeURIComponent(name)}`),
  parseWorkflow: (source: string, name = "", mode: Mode = "") =>
    req<ParseResult>("POST", "/workflows/parse", { source, name, mode }),

  tasks: () => req<Task[]>("GET", "/tasklib"),
  task: (name: string) => req<Task>("GET", `/tasklib/${encodeURIComponent(name)}`),
  createSavedTask: (t: Partial<Task>) => req<Task>("POST", "/tasklib", t),
  saveTask: (name: string, t: Partial<Task>) => req<Task>("PUT", `/tasklib/${encodeURIComponent(name)}`, t),
  deleteTask: (name: string) => req("DELETE", `/tasklib/${encodeURIComponent(name)}`),
  /** Skills in a directory's code (searches the whole repository it belongs to). */
  skills: (node: string, path: string) =>
    req<SkillScan>("GET", `/skills?node=${encodeURIComponent(node)}&path=${encodeURIComponent(path)}`),

  dirs: (path?: string, node?: string) => {
    const q = new URLSearchParams();
    if (path) q.set("path", path);
    if (node) q.set("node", node);
    return req<{ path: string; parent: string; dirs: string[] }>("GET", `/fs?${q}`);
  },

  nodes: () => req<{ nodes: NodeInfo[]; default: string }>("GET", "/nodes"),
  nodeJoin: () => req<{ hub: string; token: string }>("GET", "/nodes/join"),
  forgetNode: (id: string) => req("DELETE", `/nodes/${encodeURIComponent(id)}`),

  settings: () => req<SettingsView>("GET", "/settings"),
  saveSettings: (patch: unknown) => req<SettingsView>("PUT", "/settings", patch),
  generateSSHKey: () => req<SettingsView>("POST", "/settings/ssh-key"),
  testGit: (nodeId: string, repo: string) => req<CheckResult>("POST", "/settings/test-git", { nodeId, repo }),
  testCodex: (nodeId: string) => req<CheckResult>("POST", "/settings/test-codex", { nodeId }),

  files: (node: string, path: string) =>
    req<FileList>("GET", `/files?node=${encodeURIComponent(node)}&path=${encodeURIComponent(path)}`),
  fileContent: (node: string, path: string) =>
    req<FileView>("GET", `/files/content?node=${encodeURIComponent(node)}&path=${encodeURIComponent(path)}`),
  rawFileURL: (node: string, path: string, download = false) =>
    `/api/files/raw?node=${encodeURIComponent(node)}&path=${encodeURIComponent(path)}${download ? "&download=1" : ""}`,

  git: (sessionId: string) => req<GitStatus>("GET", `/sessions/${encodeURIComponent(sessionId)}/git`),
  gitSync: (sessionId: string, note: string) =>
    req<LaunchResult>("POST", `/sessions/${encodeURIComponent(sessionId)}/git/sync`, { note }),
};
