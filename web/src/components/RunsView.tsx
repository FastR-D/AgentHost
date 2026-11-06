import { useEffect, useMemo, useRef, useState } from "react";
import clsx from "clsx";
import { Bot, Eraser, LayoutList, Loader2, Trash2, Users, Workflow as WorkflowIcon } from "lucide-react";
import { api } from "../api";
import { guard, isTaskRun, navigate, nodeName, toast, useCurrentNode, useStore } from "../store";
import type { Run, RunStats } from "../types";
import { isActive } from "../types";
import { StatusBadge, Elapsed, fmtTime, statusLabel } from "../lib/format";
import { Sheet } from "./DirPicker";
import { ProgressBar, RunControls, progressUnits } from "./StepCard";

const filters = [
  { key: "all", label: "全部" },
  { key: "active", label: "进行中" },
  { key: "completed", label: "已完成" },
  { key: "failed", label: "失败" },
  { key: "stopped", label: "已停止" },
] as const;
type Filter = (typeof filters)[number]["key"];

const PAGE = 30;

const matches = (r: Run, f: Filter) => (f === "all" ? true : f === "active" ? isActive(r.status) : r.status === f);

/** Delete old records, now or automatically. */
function CleanupSheet({ node, onDone, onClose }: { node: string; onDone: () => void; onClose: () => void }) {
  const options = [
    { days: 7, label: "删除 7 天前的" },
    { days: 30, label: "删除 30 天前的" },
    { days: 0, label: "删除全部已结束的" },
  ];
  const [counts, setCounts] = useState<Record<number, number>>({});
  const [busy, setBusy] = useState<number | null>(null);
  const [keep, setKeep] = useState<number | null>(null);
  useEffect(() => {
    for (const o of options) api.cleanupRuns(node, o.days, true).then((r) => setCounts((c) => ({ ...c, [o.days]: r.count })));
    api.settings().then((s) => setKeep(s.runs?.retentionDays ?? 0));
  }, [node]);

  const clean = async (days: number) => {
    const n = counts[days] ?? 0;
    if (!n || !confirm(`删除「${nodeName(node)}」上 ${n} 条已结束的运行记录？它们的对话内容和日志也会删除，不能恢复。`)) return;
    setBusy(days);
    const r = await guard(api.cleanupRuns(node, days));
    setBusy(null);
    if (r) {
      toast(`已删除 ${r.count} 条`, "info");
      onDone();
      onClose();
    }
  };
  const saveKeep = async (days: number) => {
    const res = await guard(api.saveSettings({ runs: { retentionDays: days } }));
    if (res) {
      setKeep(days);
      toast(days ? `已设置：自动删除 ${days} 天前的记录` : "已关闭自动清理", "info");
    }
  };

  return (
    <Sheet
      title={
        <span className="flex items-center gap-2">
          <Eraser className="h-4 w-4 text-info" /> 清理运行记录
        </span>
      }
      onClose={onClose}
    >
      <div className="space-y-4 p-4">
        <div className="space-y-1.5">
          {options.map((o) => (
            <button
              key={o.days}
              disabled={!counts[o.days] || busy !== null}
              onClick={() => clean(o.days)}
              className="flex w-full items-center gap-2 rounded-xl border border-line px-3 py-3 text-left text-[14px] hover:border-bad/40 hover:bg-bad/5 disabled:opacity-50 disabled:hover:border-line disabled:hover:bg-transparent"
            >
              {busy === o.days ? <Loader2 className="h-4 w-4 animate-spin" /> : <Trash2 className="h-4 w-4 text-muted" />}
              <span className="flex-1">{o.label}</span>
              <span className="text-[12.5px] text-faint">{counts[o.days] === undefined ? "…" : `${counts[o.days]} 条`}</span>
            </button>
          ))}
          <p className="px-1 text-[11.5px] leading-relaxed text-faint">
            只删除这台服务器上已经结束的任务；正在运行的不动。某个会话的记录全删光时，会话也一起删除。
          </p>
        </div>
        <div className="border-t border-line pt-3">
          <div className="mb-2 text-[13px] font-medium">自动清理（所有服务器）</div>
          <div className="grid grid-cols-4 gap-1.5">
            {[0, 7, 30, 90].map((d) => (
              <button
                key={d}
                disabled={keep === null}
                onClick={() => saveKeep(d)}
                className={clsx(
                  "rounded-lg border py-2 text-[13px]",
                  keep === d ? "border-info bg-info/10 font-medium text-info" : "border-line hover:bg-sunken",
                )}
              >
                {d ? `保留 ${d} 天` : "不清理"}
              </button>
            ))}
          </div>
          <p className="mt-1.5 px-1 text-[11.5px] text-faint">开启后，每小时检查一次，删除结束时间早于这个天数的记录。</p>
        </div>
      </div>
    </Sheet>
  );
}

/** Every task run on the current server, newest first, a page at a time. */
export function RunsView() {
  const live = useStore((s) => s.runs);
  const deleted = useStore((s) => s.deletedRuns);
  const sessions = useStore((s) => s.sessions);
  const current = useCurrentNode();
  const [filter, setFilter] = useState<Filter>("all");
  const [fetched, setFetched] = useState<Run[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(false);
  const [stats, setStats] = useState<RunStats | null>(null);
  const [cleanup, setCleanup] = useState(false);
  const gen = useRef(0);

  const load = async (more: boolean) => {
    const g = more ? gen.current : ++gen.current;
    setLoading(true);
    try {
      const before = more && fetched.length ? fetched[fetched.length - 1].createdAt : undefined;
      const page = await api.runs({ node: current, tasks: true, status: filter === "all" ? undefined : filter, limit: PAGE, before });
      if (g !== gen.current) return; // the filter or server changed meanwhile
      setFetched(more ? [...fetched, ...page] : page);
      setHasMore(page.length === PAGE);
    } catch (e: any) {
      toast(e.message);
    } finally {
      if (g === gen.current) setLoading(false);
    }
  };
  const loadStats = () => api.runStats(current).then(setStats).catch(() => undefined);
  useEffect(() => {
    setFetched([]);
    void load(false);
    void loadStats();
  }, [current, filter]);

  // Refresh the counts when runs start, finish or go away.
  const liveKey = useMemo(
    () => Object.values(live).filter((r) => r.nodeId === current && isTaskRun(r)).map((r) => r.id + r.status).join() + Object.keys(deleted).length,
    [live, deleted, current],
  );
  useEffect(() => {
    const t = setTimeout(loadStats, 600);
    return () => clearTimeout(t);
  }, [liveKey]);

  // Pages from the server, kept fresh with live updates; new runs appear on top.
  const list = useMemo(() => {
    const byId = new Map<string, Run>();
    for (const r of fetched) byId.set(r.id, live[r.id] ?? r);
    const oldest = fetched.length ? fetched[fetched.length - 1].createdAt : "";
    for (const r of Object.values(live)) {
      if (r.nodeId !== current || !isTaskRun(r) || byId.has(r.id)) continue;
      if (hasMore && oldest && r.createdAt < oldest) continue; // would leave a gap
      byId.set(r.id, r);
    }
    return [...byId.values()]
      .filter((r) => !deleted[r.id] && matches(r, filter))
      .sort((a, b) => Date.parse(b.createdAt) - Date.parse(a.createdAt));
  }, [fetched, live, deleted, current, filter, hasMore]);

  const open = (r: Run) => {
    sessionStorage.setItem("agenthost.scrollTo", r.id);
    navigate({ page: "session", id: r.sessionId });
  };
  const remove = async (r: Run) => {
    if (!confirm(`删除运行记录「${r.title}」？对话里的这次任务和它的日志会一起删除。`)) return;
    if (await guard(api.deleteRun(r.id))) setFetched((l) => l.filter((x) => x.id !== r.id));
  };
  const count = (k: Filter) => (k === "all" ? stats?.total : k === "active" ? stats?.active : stats?.counts[k]);

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-5xl px-3 py-4 md:px-6 md:py-6">
        <div className="mb-4 flex items-center gap-3">
          <LayoutList className="hidden h-5 w-5 md:block" />
          <h1 className="hidden text-lg font-semibold md:block">运行记录</h1>
          <div className="flex min-w-0 overflow-x-auto rounded-lg bg-sunken p-0.5 text-[12.5px] [scrollbar-width:none]">
            {filters.map((f) => (
              <button
                key={f.key}
                onClick={() => setFilter(f.key)}
                className={clsx("shrink-0 rounded-md px-3 py-1.5 md:py-1", filter === f.key ? "bg-bg shadow-sm" : "text-muted hover:text-fg")}
              >
                {f.label}
                {count(f.key) ? <span className="ml-1 text-faint">{count(f.key)}</span> : null}
              </button>
            ))}
          </div>
          <button className="btn-ghost ml-auto shrink-0 !px-2" onClick={() => setCleanup(true)} title="清理旧记录">
            <Eraser className="h-4 w-4" /> <span className="hidden sm:inline">清理</span>
          </button>
        </div>

        {list.length === 0 && !loading && (
          <div className="rounded-xl border border-dashed border-line p-10 text-center text-faint">
            {filter === "all" ? "这台服务器上还没有运行记录" : "没有符合条件的记录"}
          </div>
        )}

        <div className="space-y-2">
          {list.map((r) => {
            const active = isActive(r.status);
            const session = sessions[r.sessionId];
            const units = progressUnits(r);
            return (
              <div
                key={r.id}
                className={clsx(
                  "rounded-xl border bg-bg p-3 transition hover:border-faint",
                  r.status === "running" ? "border-info/40" : r.status === "failed" ? "border-bad/30" : "border-line",
                )}
              >
                <div className="flex items-center gap-2">
                  {r.kind === "workflow" ? (
                    <WorkflowIcon className="h-4 w-4 shrink-0 text-info" />
                  ) : (
                    <Bot className="h-4 w-4 shrink-0 text-muted" />
                  )}
                  <button onClick={() => open(r)} className="min-w-0 flex-1 truncate text-left font-medium hover:underline">
                    {r.title}
                  </button>
                  <StatusBadge status={r.status} />
                </div>
                <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-[12px] text-muted">
                  <span>
                    {fmtTime(r.createdAt)} · <Elapsed start={r.startedAt} end={r.endedAt} running={active} />
                  </span>
                  {(r.parallel ?? 0) > 1 && (
                    <span className="chip !border-info/30 !text-info">
                      <Users className="h-3 w-3" /> {r.parallel} 个 Codex
                    </span>
                  )}
                  {r.taskTitle && <span className="chip max-w-[60vw] truncate">任务 · {r.taskTitle}</span>}
                  {session && session.title !== r.title && <span className="max-w-full truncate">会话「{session.title}」</span>}
                </div>
                <div className="truncate font-mono text-[11.5px] text-faint">{r.cwd}</div>
                {(r.kind === "workflow" || units.length > 1) && (
                  <div className="mt-2 flex flex-wrap items-center gap-1">
                    {units.map((s, i) => (
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
                  {!active && (
                    <button className="btn-ghost !px-1.5 !py-0.5 text-[12px] hover:!text-bad" onClick={() => remove(r)} title="删除这条记录">
                      <Trash2 className="h-3.5 w-3.5" /> 删除
                    </button>
                  )}
                  <button className="btn-ghost ml-auto text-[12px]" onClick={() => open(r)}>
                    打开 →
                  </button>
                </div>
              </div>
            );
          })}
        </div>

        <div className="py-4 text-center text-[12.5px] text-faint">
          {loading ? (
            <Loader2 className="mx-auto h-4 w-4 animate-spin" />
          ) : hasMore ? (
            <button className="btn mx-auto" onClick={() => load(true)}>
              加载更多
            </button>
          ) : (
            list.length > 0 && `共 ${list.length} 条`
          )}
        </div>
      </div>
      {cleanup && (
        <CleanupSheet
          node={current}
          onClose={() => setCleanup(false)}
          onDone={() => {
            void load(false);
            void loadStats();
          }}
        />
      )}
    </div>
  );
}
