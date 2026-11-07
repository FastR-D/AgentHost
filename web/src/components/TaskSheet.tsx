import { useEffect, useState } from "react";
import clsx from "clsx";
import { Check, ChevronRight, ClipboardList, Loader2, Play, Plus } from "lucide-react";
import { api } from "../api";
import { navigate, toast, useCurrentNode } from "../store";
import type { Task } from "../types";
import { Sheet } from "./DirPicker";
import { ParallelChip, isMultiParallel, parallelLabel } from "./Parallel";

let cache: Task[] | null = null;

/** Saved tasks, cached across pickers; refreshed whenever one mounts. */
export function useTasks() {
  const [tasks, setTasks] = useState<Task[] | null>(cache);
  useEffect(() => {
    api
      .tasks()
      .then((l) => {
        cache = l;
        setTasks(l);
      })
      .catch(() => setTasks(cache ?? []));
  }, []);
  return tasks;
}

export function invalidateTasks() {
  cache = null;
}

/** Tasks on a server: its own, plus those not tied to any server (the examples). */
export const tasksFor = (list: Task[], node: string) => list.filter((t) => !t.node || t.node === node);

/** Where a task runs on its server, for display. */
export const taskWhere = (t: Pick<Task, "node" | "cwd">) => t.cwd || (t.node ? "" : "所有服务器通用");

/** Choose a saved task, optionally add a note, and run it. */
export function TaskSheet({
  initial,
  initialText = "",
  where,
  onSelect,
  onRun,
  onClose,
}: {
  initial?: string;
  initialText?: string;
  /** Location chip for a new session; omitted when running in an existing one. */
  where?: React.ReactNode;
  onSelect?: (t: Task) => void;
  onRun: (task: string, note: string, parallel: number) => Promise<boolean>;
  onClose: () => void;
}) {
  const all = useTasks();
  const current = useCurrentNode();
  const tasks = all && tasksFor(all, current);
  const [sel, setSel] = useState(initial ?? "");
  const [text, setText] = useState(initialText);
  const [busy, setBusy] = useState(false);
  const [expanded, setExpanded] = useState<string | null>(null);
  const [parallel, setParallel] = useState(1);

  const select = (t: Task) => {
    setSel(t.name);
    setParallel(t.parallel ?? 1);
    onSelect?.(t);
  };
  // Apply the preselected task's settings once the list arrives.
  useEffect(() => {
    const t = initial && tasks?.find((x) => x.name === initial);
    if (t) {
      setParallel(t.parallel ?? 1);
      onSelect?.(t);
    }
  }, [!!all]);

  const run = async () => {
    if (!sel) return;
    setBusy(true);
    const ok = await onRun(sel, text.trim(), parallel);
    setBusy(false);
    if (ok) onClose();
  };

  return (
    <Sheet
      title={
        <span className="flex items-center gap-2">
          <ClipboardList className="h-4 w-4 text-info" /> 运行任务
        </span>
      }
      onClose={onClose}
      footer={
        <>
          <button className="btn mr-auto" onClick={() => navigate({ page: "tasks", name: "new" })}>
            <Plus className="h-4 w-4" /> 新任务
          </button>
          <button className="btn-primary" disabled={!sel || busy} onClick={run}>
            {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <Play className="h-4 w-4" />} 开始
          </button>
        </>
      }
    >
      <div className="space-y-1 p-3">
        {tasks === null && (
          <div className="flex items-center gap-2 p-3 text-[13px] text-faint">
            <Loader2 className="h-4 w-4 animate-spin" /> 加载中…
          </div>
        )}
        {tasks?.length === 0 && (
          <div className="p-3 text-[13px] text-muted">这台服务器的任务库还是空的。把常做的事写成一段话存起来，以后点一下就能运行。</div>
        )}
        {tasks?.map((t) => {
          const at = taskWhere(t);
          return (
            <div
              key={t.name}
              className={clsx("rounded-xl border transition", sel === t.name ? "border-info bg-info/5" : "border-transparent hover:bg-sunken")}
            >
              <button className="flex w-full items-start gap-2.5 px-3 py-2.5 text-left" onClick={() => select(t)}>
                <span
                  className={clsx(
                    "mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-full border",
                    sel === t.name ? "border-info bg-info text-bg" : "border-line",
                  )}
                >
                  {sel === t.name && <Check className="h-3 w-3" />}
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block text-[14px] font-medium">{t.title}</span>
                  {(at || isMultiParallel(t.parallel)) && (
                    <span className="block truncate font-mono text-[11.5px] text-faint">
                      {[at, isMultiParallel(t.parallel) ? parallelLabel(t.parallel) : ""].filter(Boolean).join(" · ")}
                    </span>
                  )}
                  <span className="line-clamp-2 text-[12.5px] text-muted">{t.body}</span>
                </span>
              </button>
              {sel === t.name && (
                <div className="px-3 pb-2.5 pl-9">
                  <button
                    className="flex items-center gap-1 text-[12px] text-faint hover:text-fg"
                    onClick={() => setExpanded(expanded === t.name ? null : t.name)}
                  >
                    <ChevronRight className={clsx("h-3 w-3 transition-transform", expanded === t.name && "rotate-90")} />
                    完整内容
                  </button>
                  {expanded === t.name && (
                    <div className="mt-1 max-h-64 overflow-auto whitespace-pre-wrap rounded-lg bg-panel p-2.5 text-[12.5px] text-muted">{t.body}</div>
                  )}
                </div>
              )}
            </div>
          );
        })}
      </div>
      <div className="space-y-2 border-t border-line p-3">
        <textarea
          className="input min-h-[72px] resize-none"
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder="补充说明（可选），会接在任务内容后面，例如：只看 src 目录"
        />
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          {where}
          <ParallelChip value={parallel} onChange={setParallel} />
        </div>
      </div>
    </Sheet>
  );
}

/** Starts a saved task in an existing session. */
export async function runTaskInSession(sessionId: string, task: string, note: string, parallel: number) {
  try {
    await api.chat(sessionId, note, { task, parallel });
    return true;
  } catch (e: any) {
    toast(e.message);
    return false;
  }
}
