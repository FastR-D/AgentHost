import { useState } from "react";
import clsx from "clsx";
import { Loader2, Pause, Play, RotateCcw, ScrollText, Square, StepForward } from "lucide-react";
import type { Run } from "../types";
import { isActive } from "../types";
import { useStore, control, openLogs } from "../store";

export function useRun(runId: string): Run | undefined {
  return useStore((s) => s.runs[runId]);
}

/** A task that ended without finishing can be picked up on its own Codex thread. */
export const canContinue = (run: Run) =>
  run.kind !== "manager" && (run.status === "interrupted" || run.status === "stopped" || run.status === "failed");

/** Pause / resume / stop / continue / retry / logs buttons for a run. */
export function RunControls({ run, size = "sm" }: { run: Run; size?: "sm" | "xs" }) {
  const active = isActive(run.status);
  const cls = size === "xs" ? "btn-ghost !px-1.5 !py-0.5 text-[12px]" : "btn !py-1";
  const [busy, setBusy] = useState<string | null>(null);
  const act = async (a: "pause" | "resume" | "stop" | "retry" | "continue") => {
    setBusy(a);
    await control(run.id, a);
    setBusy(null);
  };
  return (
    <div className="flex flex-wrap items-center gap-1">
      {active && run.status !== "paused" && (
        <button className={cls} onClick={() => act("pause")} disabled={!!busy} title="暂停任务">
          {busy === "pause" ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Pause className="h-3.5 w-3.5" />} 暂停
        </button>
      )}
      {run.status === "paused" && (
        <button className={cls} onClick={() => act("resume")} disabled={!!busy} title="继续任务">
          {busy === "resume" ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />} 继续
        </button>
      )}
      {active && (
        <button className={cls} onClick={() => act("stop")} disabled={!!busy} title="停止任务">
          {busy === "stop" ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Square className="h-3.5 w-3.5" />} 停止
        </button>
      )}
      {canContinue(run) && (
        <button className={cls} onClick={() => act("continue")} disabled={!!busy} title="在原来的 Codex 会话上接着做">
          {busy === "continue" ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <StepForward className="h-3.5 w-3.5" />} 继续
        </button>
      )}
      {!active && run.kind !== "manager" && (
        <button className={cls} onClick={() => act("retry")} disabled={!!busy} title="用相同输入重新运行">
          <RotateCcw className="h-3.5 w-3.5" /> 重新运行
        </button>
      )}
      <button className={cls} onClick={() => openLogs(run.id)} title="查看完整运行日志">
        <ScrollText className="h-3.5 w-3.5" /> 日志
      </button>
    </div>
  );
}

/** The steps Codex reported with report_progress, for progress display
 *  (one unit when it reported none). */
export function progressUnits(run: Run): { title: string; status: string }[] {
  const st = run.steps[0];
  if (!st) return [];
  const plan = st.plan ?? [];
  if (!plan.length) return [{ title: st.title, status: st.status }];
  // Once the run is over, steps still marked running/pending did not finish.
  const over = !["running", "paused", "pending", "queued"].includes(st.status);
  return plan.map((p) => ({
    title: p.step,
    status: over && (p.status === "running" || p.status === "pending") ? (st.status === "completed" ? "completed" : "stopped") : p.status,
  }));
}

export function ProgressBar({ run }: { run: Run }) {
  const units = progressUnits(run);
  const total = units.length || 1;
  const done = units.filter((s) => ["completed", "failed", "skipped", "stopped"].includes(s.status)).length;
  const failed = units.filter((s) => s.status === "failed").length;
  return (
    <div className="flex items-center gap-2">
      <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-sunken">
        <div
          className={clsx(
            "h-full rounded-full transition-all",
            run.status === "failed"
              ? "bg-bad"
              : run.status === "completed"
                ? failed
                  ? "bg-warn"
                  : "bg-ok"
                : failed
                  ? "bg-warn"
                  : run.status === "paused"
                    ? "bg-warn"
                    : "bg-info",
          )}
          title={failed ? `${failed} 个步骤失败` : undefined}
          style={{ width: `${(done / total) * 100}%` }}
        />
      </div>
      <span className="shrink-0 text-[11px] tabular-nums text-muted">
        {done}/{units.length} 步骤
      </span>
    </div>
  );
}
