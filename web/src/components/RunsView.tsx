import { useMemo, useState } from "react";
import clsx from "clsx";
import { Bot, LayoutList, Workflow as WorkflowIcon } from "lucide-react";
import { useStore, navigate, useCurrentNode } from "../store";
import type { Run } from "../types";
import { isActive } from "../types";
import { StatusBadge, Elapsed, fmtTime, statusLabel } from "../lib/format";
import { ProgressBar, RunControls, progressUnits } from "./StepCard";

const filters = [
  { key: "all", label: "全部" },
  { key: "active", label: "进行中" },
  { key: "completed", label: "已完成" },
  { key: "failed", label: "失败" },
  { key: "stopped", label: "已停止" },
] as const;

/** Every run across sessions, with status, progress and remote controls. */
export function RunsView() {
  const runs = useStore((s) => s.runs);
  const sessions = useStore((s) => s.sessions);
  const [filter, setFilter] = useState<(typeof filters)[number]["key"]>("all");
  const [showChildren, setShowChildren] = useState(false);
  const current = useCurrentNode();

  const list = useMemo(() => {
    return Object.values(runs)
      .filter((r) => r.nodeId === current && r.kind !== "manager")
      .filter((r) => showChildren || !r.parentRunId)
      .filter((r) => {
        if (filter === "all") return true;
        if (filter === "active") return isActive(r.status);
        return r.status === filter;
      })
      .sort((a, b) => Date.parse(b.createdAt) - Date.parse(a.createdAt));
  }, [runs, filter, showChildren, current]);

  const counts = useMemo(() => {
    const c: Record<string, number> = {};
    for (const r of Object.values(runs)) {
      if (r.parentRunId || r.nodeId !== current || r.kind === "manager") continue;
      c[r.status] = (c[r.status] ?? 0) + 1;
      if (isActive(r.status)) c.active = (c.active ?? 0) + 1;
    }
    return c;
  }, [runs, current]);

  const hasChildren = useMemo(() => Object.values(runs).some((r) => r.parentRunId), [runs]);

  const open = (r: Run) => {
    const target = r.parentRunId ? runs[r.parentRunId] ?? r : r;
    sessionStorage.setItem("agenthost.scrollTo", target.id);
    navigate({ page: "session", id: r.sessionId });
  };

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-5xl px-3 py-4 md:px-6 md:py-6">
        <div className="mb-4 flex flex-wrap items-center gap-3">
          <LayoutList className="hidden h-5 w-5 md:block" />
          <h1 className="hidden text-lg font-semibold md:block">运行记录</h1>
          <div className="flex max-w-full overflow-x-auto rounded-lg bg-sunken p-0.5 text-[12.5px] [scrollbar-width:none]">
            {filters.map((f) => (
              <button
                key={f.key}
                onClick={() => setFilter(f.key)}
                className={clsx("shrink-0 rounded-md px-3 py-1.5 md:py-1", filter === f.key ? "bg-bg shadow-sm" : "text-muted hover:text-fg")}
              >
                {f.label}
                {f.key !== "all" && counts[f.key] ? <span className="ml-1 text-faint">{counts[f.key]}</span> : null}
              </button>
            ))}
          </div>
          {hasChildren && (
            <label className="ml-auto flex items-center gap-1.5 text-[12.5px] text-muted">
              <input type="checkbox" checked={showChildren} onChange={(e) => setShowChildren(e.target.checked)} />
              显示子 workflow
            </label>
          )}
        </div>

        {list.length === 0 && <div className="rounded-xl border border-dashed border-line p-10 text-center text-faint">还没有运行记录</div>}

        <div className="space-y-2">
          {list.map((r) => {
            const active = isActive(r.status);
            const session = sessions[r.sessionId];
            return (
              <div
                key={r.id}
                className={clsx(
                  "rounded-xl border bg-bg p-3 transition hover:border-faint",
                  r.status === "running" ? "border-info/40" : r.status === "failed" ? "border-bad/30" : "border-line",
                )}
              >
                <div className="flex flex-wrap items-center gap-2">
                  {r.kind === "workflow" ? (
                    <WorkflowIcon className="h-4 w-4 shrink-0 text-info" />
                  ) : (
                    <Bot className="h-4 w-4 shrink-0 text-muted" />
                  )}
                  <button onClick={() => open(r)} className="min-w-0 truncate text-left font-medium hover:underline">
                    {r.title}
                  </button>
                  <StatusBadge status={r.status} />
                  {r.parentRunId && <span className="chip">子 workflow</span>}
                  {r.taskTitle && <span className="chip max-w-[60vw] truncate">任务 · {r.taskTitle}</span>}
                  <span className="ml-auto text-[12px] text-faint">
                    {fmtTime(r.createdAt)} · <Elapsed start={r.startedAt} end={r.endedAt} running={active} />
                  </span>
                </div>
                <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-[12px] text-muted">
                  <span className="truncate">会话「{session?.title ?? r.sessionId}」</span>
                  <span className="truncate font-mono text-faint">{r.cwd}</span>
                </div>
                {(r.kind === "workflow" || progressUnits(r).length > 1) && (
                  <div className="mt-2 flex flex-wrap items-center gap-1">
                    {progressUnits(r).map((s, i) => (
                      <span
                        key={i}
                        title={`${s.title}：${statusLabel[s.status]}`}
                        className={clsx(
                          "h-1.5 w-6 rounded-full",
                          s.status === "completed" && "bg-ok",
                          s.status === "failed" && "bg-bad",
                          s.status === "running" && "animate-pulse bg-info",
                          s.status === "paused" && "bg-warn",
                          ["pending", "skipped", "stopped", "queued"].includes(s.status) && "bg-sunken",
                        )}
                      />
                    ))}
                    <div className="ml-2 min-w-[140px] flex-1">
                      <ProgressBar run={r} />
                    </div>
                  </div>
                )}
                {active && r.activity && <div className="mt-1.5 truncate text-[12.5px] text-info">{r.activity}</div>}
                {r.status === "failed" && r.error && <div className="mt-1.5 truncate text-[12.5px] text-bad">{r.error}</div>}
                <div className="mt-2 flex items-center gap-2">
                  <RunControls run={r} size="xs" />
                  <button className="btn-ghost ml-auto text-[12px]" onClick={() => open(r)}>
                    打开会话 →
                  </button>
                </div>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
