import { create } from "zustand";
import { api } from "./api";
import type { Info, Item, LogEntry, NodeInfo, Run, Session, Step } from "./types";

export type Route =
  | { page: "home" }
  | { page: "session"; id: string }
  | { page: "runs" }
  | { page: "tasks"; name?: string }
  | { page: "files"; node?: string; path?: string; file?: string }
  | { page: "servers" }
  | { page: "settings" };

export function parseHash(hash: string): Route {
  const raw = hash.replace(/^#\/?/, "");
  const [h, query = ""] = raw.split("?");
  const q = new URLSearchParams(query);
  const [a, b] = h.split("/").map((x) => decodeURIComponent(x));
  if (a === "s" && b) return { page: "session", id: b };
  if (a === "runs") return { page: "runs" };
  // #/skills was the old name of the task library.
  if (a === "tasks" || a === "skills") return { page: "tasks", name: b || undefined };
  if (a === "files")
    return { page: "files", node: q.get("node") || undefined, path: q.get("path") || undefined, file: q.get("file") || undefined };
  if (a === "servers") return { page: "servers" };
  if (a === "settings") return { page: "settings" };
  return { page: "home" };
}

export function routeHash(route: Route): string {
  switch (route.page) {
    case "session":
      return `#/s/${encodeURIComponent(route.id)}`;
    case "runs":
      return "#/runs";
    case "tasks":
      return route.name ? `#/tasks/${encodeURIComponent(route.name)}` : "#/tasks";
    case "files": {
      const q = new URLSearchParams();
      if (route.node) q.set("node", route.node);
      if (route.path) q.set("path", route.path);
      if (route.file) q.set("file", route.file);
      const qs = q.toString();
      return `#/files${qs ? `?${qs}` : ""}`;
    }
    case "servers":
      return "#/servers";
    case "settings":
      return "#/settings";
  }
  return "#/";
}

export function navigate(route: Route) {
  const h = routeHash(route);
  if (location.hash !== h) location.hash = h;
}

interface Toast {
  id: number;
  text: string;
  kind: "error" | "info";
}

interface State {
  info?: Info;
  sessions: Record<string, Session>;
  runs: Record<string, Run>;
  loadedSessions: Record<string, boolean>;
  logs: Record<string, LogEntry[]>;
  connected: boolean;
  toasts: Toast[];
  logsPanel?: { runId: string; stepId?: string };
  /** Run whose execution details are open. */
  processRun?: string;
  /** Runs deleted while this page was open (lists drop them). */
  deletedRuns: Record<string, true>;
  nodes: NodeInfo[];
  defaultNode: string;
  /** The server the user is working on; every page shows only its things. */
  currentNode: string;
}

const NODE_KEY = "agenthost.node";
function savedNode(): string {
  try {
    return localStorage.getItem(NODE_KEY) ?? "";
  } catch {
    return "";
  }
}

export const useStore = create<State>(() => ({
  currentNode: savedNode(),
  deletedRuns: {},
  sessions: {},
  runs: {},
  loadedSessions: {},
  logs: {},
  connected: false,
  toasts: [],
  nodes: [],
  defaultNode: "local",
}));

const set = useStore.setState;
const get = useStore.getState;

let toastSeq = 0;
export function toast(text: string, kind: "error" | "info" = "error") {
  const id = ++toastSeq;
  set((s) => ({ toasts: [...s.toasts, { id, text, kind }] }));
  setTimeout(() => set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) })), kind === "error" ? 6000 : 3000);
}

export async function guard<T>(p: Promise<T>): Promise<T | undefined> {
  try {
    return await p;
  } catch (e: any) {
    toast(e?.message ?? String(e));
    return undefined;
  }
}

// ---------------------------------------------------------------- merging

const truncated = (s?: string) => !!s && s.endsWith("…");

function mergeRun(old: Run | undefined, next: Run): Run {
  if (!old) {
    return { ...next, steps: next.steps.map((st) => ({ ...st, items: st.items ?? [] })) };
  }
  const steps = next.steps.map((st) => {
    const os = old.steps.find((x) => x.id === st.id);
    if (!os) return { ...st, items: st.items ?? [] };
    return {
      ...st,
      items: st.items ?? os.items ?? [],
      prompt: truncated(st.prompt) && (os.prompt?.length ?? 0) > (st.prompt?.length ?? 0) ? os.prompt : st.prompt,
      output: truncated(st.output) && (os.output?.length ?? 0) > (st.output?.length ?? 0) ? os.output : st.output,
      diff: st.diff || os.diff,
    };
  });
  return {
    ...old,
    ...next,
    source: next.source ?? old.source,
    workflow: next.workflow ?? old.workflow,
    steps,
  };
}

function updateStep(run: Run, stepId: string, fn: (st: Step) => Step): Run {
  const idx = run.steps.findIndex((s) => s.id === stepId);
  if (idx < 0) return run;
  const steps = run.steps.slice();
  steps[idx] = fn(steps[idx]);
  return { ...run, steps };
}

function upsertItem(st: Step, item: Item): Step {
  const items = (st.items ?? []).slice();
  const i = items.findIndex((x) => x.id === item.id);
  if (i >= 0) items[i] = item;
  else items.push(item);
  return { ...st, items, itemCount: items.length };
}

const MAX_TEXT = 512 * 1024;
const capTail = (s: string) => (s.length > MAX_TEXT ? s.slice(s.length - MAX_TEXT) : s);

function appendDelta(st: Step, itemId: string, field: string, delta: string): Step {
  const items = (st.items ?? []).slice();
  let i = items.findIndex((x) => x.id === itemId);
  if (i < 0) {
    const kind = field === "text" ? "message" : "command";
    items.push({ id: itemId, kind, status: "running" } as Item);
    i = items.length - 1;
  }
  const it = { ...items[i] };
  if (field === "text") it.text = (it.text ?? "") + delta;
  else if (field === "output") it.output = capTail((it.output ?? "") + delta);
  else if (field === "stdout") {
    it.stdout = capTail((it.stdout ?? "") + delta);
    it.output = capTail((it.output ?? "") + delta);
  } else if (field === "stderr") {
    it.stderr = capTail((it.stderr ?? "") + delta);
    it.output = capTail((it.output ?? "") + delta);
  }
  items[i] = it;
  return { ...st, items };
}

interface ServerEvent {
  seq: number;
  type: string;
  data: any;
}

function applyEvent(s: State, ev: ServerEvent): Partial<State> {
  const d = ev.data;
  switch (ev.type) {
    case "session":
      return { sessions: { ...s.sessions, [d.id]: { ...s.sessions[d.id], ...d } } };
    case "session.deleted": {
      const sessions = { ...s.sessions };
      delete sessions[d.id];
      const runs = Object.fromEntries(Object.entries(s.runs).filter(([, r]) => r.sessionId !== d.id));
      return { sessions, runs };
    }
    case "run":
      return { runs: { ...s.runs, [d.id]: mergeRun(s.runs[d.id], d) } };
    case "run.deleted": {
      const runs = { ...s.runs };
      delete runs[d.id];
      return { runs, deletedRuns: { ...s.deletedRuns, [d.id]: true } };
    }
    case "run.full":
      return { runs: { ...s.runs, [d.id]: mergeRun(s.runs[d.id], d) } };
    case "item": {
      const run = s.runs[d.runId];
      if (!run) return {};
      return { runs: { ...s.runs, [d.runId]: updateStep(run, d.stepId, (st) => upsertItem(st, d.item)) } };
    }
    case "delta": {
      const run = s.runs[d.runId];
      if (!run) return {};
      return {
        runs: { ...s.runs, [d.runId]: updateStep(run, d.stepId, (st) => appendDelta(st, d.itemId, d.field, d.delta)) },
      };
    }
    case "diff": {
      const run = s.runs[d.runId];
      if (!run) return {};
      return { runs: { ...s.runs, [d.runId]: updateStep(run, d.stepId, (st) => ({ ...st, diff: d.diff })) } };
    }
    case "nodes":
      return { nodes: d };
    case "log": {
      const list = s.logs[d.runId];
      if (!list) return {};
      const next = list.length > 20000 ? list.slice(list.length - 15000) : list.slice();
      next.push(d);
      return { logs: { ...s.logs, [d.runId]: next } };
    }
  }
  return {};
}

// ---------------------------------------------------------------- live connection

let es: EventSource | null = null;
let queue: ServerEvent[] = [];
let flushScheduled = false;

function flush() {
  flushScheduled = false;
  const evs = queue;
  queue = [];
  if (!evs.length) return;
  set((s) => {
    let cur: State = s;
    for (const ev of evs) cur = { ...cur, ...applyEvent(cur, ev) };
    return cur;
  });
}

function schedule() {
  if (flushScheduled) return;
  flushScheduled = true;
  if (document.visibilityState === "visible") requestAnimationFrame(flush);
  else setTimeout(flush, 250);
}

export function connect() {
  if (es) es.close();
  es = new EventSource("/api/events");
  es.addEventListener("hello", () => {
    set({ connected: true });
    void resync();
  });
  es.addEventListener("resync", () => void resync());
  es.onmessage = (m) => {
    try {
      queue.push(JSON.parse(m.data));
      schedule();
    } catch {
      /* ignore malformed */
    }
  };
  es.onerror = () => {
    set({ connected: false });
  };
}

export function disconnect() {
  es?.close();
  es = null;
}

/** Reload the session list, the runs summary list and every open session. */
export async function resync() {
  // Only what the sidebar needs: running tasks and the latest few. The run
  // list pages through the rest itself.
  const [info, sessions, active, recent, nodes] = await Promise.all([
    api.info(),
    api.sessions(),
    api.runs({ status: "active" }),
    api.runs({ limit: 40 }),
    api.nodes().catch(() => ({ nodes: [], default: "local" })),
  ]);
  const runs = [...active, ...recent.filter((r) => !active.some((a) => a.id === r.id))];
  const loaded = Object.keys(get().loadedSessions);
  set((s) => {
    const merged: Record<string, Run> = {};
    for (const r of runs) merged[r.id] = mergeRun(s.runs[r.id], r);
    // Keep fully-loaded runs that fell outside the summary window.
    for (const [id, r] of Object.entries(s.runs)) if (!merged[id] && s.loadedSessions[r.sessionId]) merged[id] = r;
    return {
      info,
      sessions: Object.fromEntries(sessions.map((x) => [x.id, x])),
      runs: merged,
      loadedSessions: {},
      nodes: nodes.nodes,
      defaultNode: nodes.default,
    };
  });
  await Promise.all(loaded.map((id) => loadSession(id).catch(() => undefined)));
  const panel = get().logsPanel;
  if (panel) void loadLogs(panel.runId);
}

export async function loadSession(id: string) {
  const res = await api.session(id);
  set((s) => {
    const runs = { ...s.runs };
    for (const r of res.runs) runs[r.id] = mergeRun(undefined, r);
    return {
      sessions: { ...s.sessions, [id]: res.session },
      runs,
      loadedSessions: { ...s.loadedSessions, [id]: true },
    };
  });
}

export async function loadRun(id: string) {
  const r = await api.run(id);
  set((s) => ({ runs: { ...s.runs, [id]: mergeRun(undefined, r) } }));
}

export async function loadLogs(runId: string) {
  set((s) => ({ logs: { ...s.logs, [runId]: s.logs[runId] ?? [] } }));
  const entries = await api.logs(runId);
  set((s) => {
    const live = (s.logs[runId] ?? []).filter((e) => !entries.length || e.seq > entries[entries.length - 1].seq);
    return { logs: { ...s.logs, [runId]: [...entries, ...live] } };
  });
}

export function openLogs(runId: string, stepId?: string) {
  set({ logsPanel: { runId, stepId } });
  void loadLogs(runId);
}

export const openProcess = (runId: string) => set({ processRun: runId });
export const closeProcess = () => set({ processRun: undefined });

export function closeLogs() {
  const p = get().logsPanel;
  set((s) => {
    const logs = { ...s.logs };
    if (p) delete logs[p.runId];
    return { logsPanel: undefined, logs };
  });
}

export async function control(runId: string, action: "pause" | "resume" | "stop" | "retry") {
  const res = await guard(api.control(runId, action));
  if (res && action === "retry" && res.session?.id) navigate({ page: "session", id: res.session.id });
  return res;
}

/** The task runs of a session (not the manager's replies). */
export const isTaskRun = (r: Run) => r.kind !== "manager" && !r.parentRunId;

/** Runs of a session that appear as messages, in order. */
export function sessionRuns(s: State, sessionId: string): Run[] {
  const sess = s.sessions[sessionId];
  if (!sess) return [];
  return sess.runIds.map((id) => s.runs[id]).filter(Boolean);
}

/** The current server, falling back to the default when it is gone. */
export function currentNodeOf(s: State): string {
  const c = s.currentNode;
  if (c && (s.nodes.length === 0 || s.nodes.some((n) => n.id === c))) return c;
  return s.defaultNode;
}

export const useCurrentNode = () => useStore(currentNodeOf);

export function setCurrentNode(id: string) {
  if (!id || id === currentNodeOf(get())) return;
  set({ currentNode: id });
  try {
    localStorage.setItem(NODE_KEY, id);
  } catch {
    /* private mode: remembered for this visit only */
  }
}

export function nodeName(id?: string): string {
  if (!id) return "";
  return useStore.getState().nodes.find((n) => n.id === id)?.name ?? id;
}
