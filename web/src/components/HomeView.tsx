import { useEffect, useRef, useState } from "react";
import { ArrowUp, ClipboardList, Loader2, Play } from "lucide-react";
import { api } from "../api";
import { useStore, guard, navigate, useCurrentNode } from "../store";
import { WhereChip } from "./DirPicker";
import { TaskSheet, taskWhere, tasksFor, useTasks } from "./TaskSheet";

const coarse = typeof window !== "undefined" && window.matchMedia("(pointer: coarse)").matches;

/** Landing page: describe a task in plain words, or run a saved one. */
export function HomeView() {
  const info = useStore((s) => s.info);
  const node = useCurrentNode();
  const all = useTasks();
  const tasks = all && tasksFor(all, node);
  const [text, setText] = useState("");
  const [cwd, setCwd] = useState("");
  const [busy, setBusy] = useState(false);
  const [picker, setPicker] = useState<{ task?: string; cwd: string } | null>(null);
  const openPicker = (task?: string) => setPicker({ task, cwd });
  // A directory belongs to its server.
  useEffect(() => setCwd(""), [node]);
  const ref = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    if (!coarse) ref.current?.focus();
  }, []);

  const start = async (body: { text?: string; task?: string }) => {
    const res = await guard(api.startTask({ ...body, cwd, nodeId: node }));
    if (res) navigate({ page: "session", id: res.session.id });
    return !!res;
  };

  const submit = async () => {
    if (!text.trim() || busy) return;
    setBusy(true);
    await start({ text });
    setBusy(false);
  };

  const where = <WhereChip node={node} path={cwd} onChange={(p) => setCwd(p)} />;

  return (
    <div className="flex h-full flex-col items-center overflow-y-auto px-4">
      <div className="w-full max-w-2xl pb-10 pt-[6vh] md:pt-[14vh]">
        <h1 className="mb-4 text-center text-xl font-semibold md:mb-6 md:text-2xl">要让 Codex 做什么？</h1>
        <div className="rounded-2xl border border-line bg-bg shadow-sm focus-within:border-faint">
          <textarea
            ref={ref}
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (!coarse && e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                e.preventDefault();
                void submit();
              }
            }}
            rows={3}
            placeholder="用大白话描述要做的事，例如：先跑一下测试，同时看看最近改的代码有没有问题，最后给我一份中文报告"
            className="w-full resize-none bg-transparent px-4 pt-4 text-[14px] outline-none placeholder:text-faint"
          />
          <div className="flex flex-wrap items-center gap-x-1 gap-y-2 px-3 pb-3">
            {where}
            <button className="btn-ghost shrink-0" onClick={() => openPicker()} title="从任务库选一个任务运行">
              <ClipboardList className="h-4 w-4" /> 任务
            </button>
            <button
              className="ml-auto flex h-9 w-9 items-center justify-center rounded-full bg-accent text-bg hover:opacity-90 disabled:opacity-30"
              disabled={!text.trim() || busy}
              onClick={submit}
              title="开始"
            >
              {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <ArrowUp className="h-4 w-4" />}
            </button>
          </div>
        </div>
        <div className="mt-2 text-center text-[12px] text-faint">
          Codex 会自己拆分步骤并实时汇报进度 · 沙箱 {info?.defaultSandbox ?? "—"} · 最多并行 {info?.maxRuns ?? "—"} 个任务
        </div>

        {tasks && tasks.length > 0 && (
          <div className="mt-8">
            <div className="mb-2 flex items-center text-[12px] font-medium text-faint">
              常用任务
              <button className="ml-auto hover:text-fg" onClick={() => navigate({ page: "tasks" })}>
                管理 →
              </button>
            </div>
            <div className="grid gap-2 sm:grid-cols-2">
              {tasks.slice(0, 6).map((t) => {
                const at = taskWhere(t);
                return (
                  <button
                    key={t.name}
                    onClick={() => openPicker(t.name)}
                    className="group rounded-xl border border-line p-3 text-left transition hover:border-faint hover:bg-panel"
                  >
                    <div className="flex items-center gap-2">
                      <ClipboardList className="h-4 w-4 shrink-0 text-info" />
                      <span className="truncate font-medium">{t.title}</span>
                      <Play className="ml-auto h-3.5 w-3.5 shrink-0 text-faint md:opacity-0 md:group-hover:opacity-100" />
                    </div>
                    {at && <div className="mt-0.5 truncate font-mono text-[11px] text-faint">{at}</div>}
                    <div className="mt-1 line-clamp-2 text-[12px] text-muted">{t.body}</div>
                  </button>
                );
              })}
            </div>
          </div>
        )}
      </div>
      {picker && (
        <TaskSheet
          initial={picker.task}
          initialText={text}
          where={where}
          onSelect={(t) => {
            // Run where the task says, unless it does not say.
            if (t.cwd) setCwd(t.cwd);
          }}
          onClose={() => {
            // Picking a task only previews its directory; closing puts back the user's choice.
            setCwd(picker.cwd);
            setPicker(null);
          }}
          onRun={(task, note) => start({ task, text: note })}
        />
      )}
    </div>
  );
}
