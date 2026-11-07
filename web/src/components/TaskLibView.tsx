import { useEffect, useRef, useState } from "react";
import clsx from "clsx";
import { ArrowLeft, ClipboardList, Loader2, Play, Plus, Save, Trash2, X } from "lucide-react";
import { api } from "../api";
import { guard, navigate, setCurrentNode, toast, useCurrentNode } from "../store";
import type { Task } from "../types";
import { fmtRelative } from "../lib/format";
import { WhereChip } from "./DirPicker";
import { invalidateTasks, taskWhere, tasksFor } from "./TaskSheet";
import { ParallelChip, isMultiParallel, parallelLabel, PARALLEL_UNLIMITED } from "./Parallel";

const EMPTY: Task = { name: "", title: "", node: "", cwd: "", parallel: 0, body: "", updatedAt: "" };

function Editor({ name, onSaved, onDeleted }: { name: string; onSaved: (t: Task) => void; onDeleted: () => void }) {
  const isNew = name === "new";
  const [t, setT] = useState<Task>(EMPTY);
  const [orig, setOrig] = useState<Task>(EMPTY);
  const [loading, setLoading] = useState(!isNew);
  const [saving, setSaving] = useState(false);
  const [running, setRunning] = useState(false);
  const body = useRef<HTMLTextAreaElement>(null);
  const current = useCurrentNode();
  // New tasks belong to the current server; the examples work on any server.
  const node = isNew ? current : t.node || current;
  const dirty = t.title !== orig.title || t.body !== orig.body || (t.node ?? "") !== (orig.node ?? "") || (t.cwd ?? "") !== (orig.cwd ?? "") || (t.parallel ?? 0) !== (orig.parallel ?? 0);

  useEffect(() => {
    if (isNew) {
      setT(EMPTY);
      setOrig(EMPTY);
      return;
    }
    setLoading(true);
    api
      .task(name)
      .then((x) => {
        setT(x);
        setOrig(x);
        if (x.node) setCurrentNode(x.node); // opened from a link to another server's task
      })
      .catch((e) => toast(e.message))
      .finally(() => setLoading(false));
  }, [name]);

  const save = async () => {
    setSaving(true);
    const fields = { title: t.title, node: isNew || t.cwd ? node : t.node ?? "", cwd: t.cwd ?? "", parallel: t.parallel ?? 0, body: t.body };
    const res = await guard(isNew ? api.createSavedTask(fields) : api.saveTask(name, fields));
    setSaving(false);
    if (res) {
      invalidateTasks();
      setT(res);
      setOrig(res);
      toast("已保存", "info");
      onSaved(res);
    }
    return res;
  };

  const remove = async () => {
    if (!confirm(`删除任务「${orig.title}」？`)) return;
    if (await guard(api.deleteTask(name))) {
      invalidateTasks();
      onDeleted();
    }
  };

  const run = async () => {
    let target = name;
    if (isNew || dirty) {
      const saved = await save();
      if (!saved) return;
      target = saved.name;
    }
    setRunning(true);
    const res = await guard(api.startTask({ task: target, nodeId: node, cwd: t.cwd ?? "" }));
    setRunning(false);
    if (res) navigate({ page: "session", id: res.session.id });
  };

  if (loading)
    return (
      <div className="flex h-full items-center justify-center text-faint">
        <Loader2 className="h-5 w-5 animate-spin" />
      </div>
    );

  const canRun = t.title.trim() && t.body.trim();
  return (
    <div className="mx-auto max-w-2xl space-y-4 p-4 pb-28 md:pb-8">
      <label className="block">
        <span className="text-[13px] text-muted">名字</span>
        <input className="input mt-1 !text-[15px]" value={t.title} onChange={(e) => setT({ ...t, title: e.target.value })} placeholder="例如：部署到测试环境" />
      </label>

      <div>
        <span className="text-[13px] text-muted">在哪个目录运行</span>
        <div className="mt-1 flex min-w-0 items-center gap-1">
          <div className="min-w-0">
            <WhereChip node={node} path={t.cwd} placeholder="不指定（运行时用工作区）" onChange={(p) => setT({ ...t, cwd: p })} />
          </div>
          {t.cwd && (
            <button className="btn-ghost shrink-0 !p-1.5" onClick={() => setT({ ...t, cwd: "" })} title="不指定">
              <X className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
        <span className="mt-1 block text-[11.5px] text-faint">一般选代码所在的目录，Codex 会在那里干活。</span>
      </div>

      <div>
        <span className="text-[13px] text-muted">同时用几个 Codex</span>
        <div className="mt-1">
          <ParallelChip value={t.parallel} onChange={(n) => setT({ ...t, parallel: n === PARALLEL_UNLIMITED ? PARALLEL_UNLIMITED : n > 1 ? n : 0 })} />
        </div>
      </div>

      <label className="block">
        <span className="text-[13px] text-muted">要做什么</span>
        <textarea
          ref={body}
          className="input mt-1 min-h-[220px] resize-y leading-relaxed"
          value={t.body}
          onChange={(e) => setT({ ...t, body: e.target.value })}
          placeholder={"用大白话写清楚要做什么、要注意什么、最后交付什么。比如：\n\n拉一下最新代码，跑测试，测试通过就部署到测试环境，最后告诉我部署了哪个版本。"}
        />
      </label>

      <div className="fixed inset-x-0 bottom-0 z-20 flex gap-2 border-t border-line bg-bg/95 px-4 py-3 pb-safe backdrop-blur md:static md:border-0 md:bg-transparent md:p-0">
        {!isNew && (
          <button className="btn hover:!text-bad" onClick={remove}>
            <Trash2 className="h-4 w-4" /> 删除
          </button>
        )}
        <button className="btn flex-1 justify-center !py-2.5 md:ml-auto md:flex-none md:!py-1.5" onClick={save} disabled={saving || (!isNew && !dirty)}>
          {saving ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />} 保存
        </button>
        <button className="btn-primary flex-1 justify-center !py-2.5 md:flex-none md:!py-1.5" onClick={run} disabled={running || saving || !canRun}>
          {running ? <Loader2 className="h-4 w-4 animate-spin" /> : <Play className="h-4 w-4" />} 运行
        </button>
      </div>
    </div>
  );
}

/** Task library: task descriptions written once and run again and again. */
export function TaskLibView({ name }: { name?: string }) {
  const [all, setList] = useState<Task[] | null>(null);
  const current = useCurrentNode();
  const list = all && tasksFor(all, current);
  const refresh = () => api.tasks().then(setList).catch((e) => toast(e.message));
  useEffect(() => {
    void refresh();
  }, []);

  return (
    <div className="flex h-full min-h-0">
      <div className={clsx("w-full flex-col border-line md:flex md:w-72 md:shrink-0 md:border-r", name ? "hidden" : "flex")}>
        <div className="flex items-center gap-2 px-4 py-3">
          <ClipboardList className="h-4 w-4 text-info" />
          <span className="font-medium">任务库</span>
          <button className="btn ml-auto !py-1" onClick={() => navigate({ page: "tasks", name: "new" })}>
            <Plus className="h-4 w-4" /> 新任务
          </button>
        </div>
        <p className="px-4 pb-2 text-[12px] leading-relaxed text-faint">
          这台服务器上常做的事，用 Markdown 写清楚怎么做，存起来，可以指定在哪个目录运行。运行时原样交给 Codex 照着做。
        </p>
        <div className="min-h-0 flex-1 space-y-1 overflow-y-auto px-2 pb-3">
          {list === null && <div className="p-3 text-[13px] text-faint">加载中…</div>}
          {list?.length === 0 && <div className="p-3 text-[13px] text-faint">这台服务器上还没有任务</div>}
          {list?.map((t) => {
            const at = taskWhere(t);
            return (
              <button
                key={t.name}
                onClick={() => navigate({ page: "tasks", name: t.name })}
                className={clsx("w-full rounded-xl px-3 py-2.5 text-left transition", name === t.name ? "bg-sunken" : "hover:bg-sunken")}
              >
                <div className="text-[14px] font-medium">{t.title}</div>
                <div className="line-clamp-2 text-[12.5px] text-muted">{t.body}</div>
                <div className="mt-0.5 truncate text-[11px] text-faint">
                  {at && <span className="font-mono">{at} · </span>}
                  {isMultiParallel(t.parallel) && <span>{parallelLabel(t.parallel)} · </span>}
                  {fmtRelative(t.updatedAt)}更新
                </div>
              </button>
            );
          })}
        </div>
      </div>
      <div className={clsx("min-w-0 flex-1 flex-col md:flex", name ? "flex" : "hidden")}>
        <div className="flex items-center gap-1 border-b border-line px-2 py-2 md:hidden">
          <button className="btn-ghost !p-1.5" onClick={() => navigate({ page: "tasks" })} aria-label="返回">
            <ArrowLeft className="h-5 w-5" />
          </button>
          <span className="text-[14px] font-medium">{name === "new" ? "新任务" : "编辑任务"}</span>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto">
          {name ? (
            <Editor
              key={name}
              name={name}
              onSaved={(t) => {
                void refresh();
                if (name === "new") navigate({ page: "tasks", name: t.name });
              }}
              onDeleted={() => {
                void refresh();
                navigate({ page: "tasks" });
              }}
            />
          ) : (
            <div className="flex h-full items-center justify-center text-[13px] text-faint">选择一个任务，或者新建一个</div>
          )}
        </div>
      </div>
    </div>
  );
}
