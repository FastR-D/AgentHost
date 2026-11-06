import { useEffect, useRef, useState } from "react";
import clsx from "clsx";
import { Bot, ChevronRight, ListTree, Loader2, Pause, Play, RotateCcw, Square } from "lucide-react";
import type { Item, Run } from "../types";
import { isActive } from "../types";
import { closeProcess, control, openProcess, useStore } from "../store";
import { Elapsed, StatusBadge, StatusIcon, between, fmtDuration, fmtTokens, statusLabel } from "../lib/format";
import { ItemView } from "./ItemView";
import { Sheet } from "./DirPicker";
import { ProgressBar, RunControls, StepCard, progressUnits, useRun } from "./StepCard";

/** What a task run did, in a few words. */
function summary(run: Run) {
  let commands = 0;
  const files = new Set<string>();
  for (const st of run.steps)
    for (const it of st.items ?? []) {
      if (it.kind === "command") commands++;
      if (it.kind === "file_change") for (const c of it.changes ?? []) files.add(c.path);
    }
  const parts: string[] = [];
  if (commands) parts.push(`${commands} 条命令`);
  if (files.size) parts.push(`改了 ${files.size} 个文件`);
  if (run.startedAt) parts.push(fmtDuration(between(run.startedAt, run.endedAt)));
  return parts.join(" · ");
}

function nowText(run: Run) {
  if (run.status === "queued") return "排队中，等待空闲执行槽位…";
  if (run.status === "paused") return "已暂停";
  return run.activity || "执行中…";
}

/** The session's task at a glance, pinned under the header: state, progress, controls. */
export function TaskBar({ run }: { run: Run }) {
  const active = isActive(run.status);
  const steps = progressUnits(run).length > 1;
  const [busy, setBusy] = useState<string | null>(null);
  const tokens = run.steps.reduce((n, s) => n + (s.usage?.total?.totalTokens ?? 0), 0);
  const act = async (a: "pause" | "resume" | "stop" | "retry") => {
    setBusy(a);
    await control(run.id, a);
    setBusy(null);
  };
  const icon = "h-3.5 w-3.5";
  return (
    <div className={clsx("border-b border-line px-3 py-2 md:px-4", active ? "bg-info/5" : "bg-panel")}>
      <div className="flex items-center gap-2">
        <button className="flex min-w-0 flex-1 items-center gap-2 text-left" onClick={() => openProcess(run.id)} title="查看执行过程">
          {run.status === "running" ? (
            <Loader2 className="h-4 w-4 shrink-0 animate-spin text-info" />
          ) : run.status === "paused" ? (
            <Pause className="h-4 w-4 shrink-0 text-warn" />
          ) : (
            <StatusIcon status={run.status} className="h-4 w-4 shrink-0" />
          )}
          <span className="min-w-0 flex-1">
            <span className={clsx("block truncate text-[13px]", run.status === "running" ? "shimmer-text font-medium" : "text-fg")}>
              {active ? nowText(run) : statusLabel[run.status]}
            </span>
            <span className="block truncate text-[11px] text-faint">
              {active ? (
                <>
                  后台执行中 · <Elapsed start={run.startedAt ?? run.createdAt} running />
                </>
              ) : (
                [summary(run), tokens ? `${fmtTokens(tokens)} tokens` : ""].filter(Boolean).join(" · ")
              )}
            </span>
          </span>
          <ChevronRight className="h-4 w-4 shrink-0 text-faint" />
        </button>
        <div className="flex shrink-0 items-center">
          {active && run.status !== "paused" && (
            <button className="btn-ghost !p-2" onClick={() => act("pause")} disabled={!!busy} title="暂停">
              {busy === "pause" ? <Loader2 className={clsx(icon, "animate-spin")} /> : <Pause className={icon} />}
            </button>
          )}
          {run.status === "paused" && (
            <button className="btn-ghost !p-2" onClick={() => act("resume")} disabled={!!busy} title="继续">
              {busy === "resume" ? <Loader2 className={clsx(icon, "animate-spin")} /> : <Play className={icon} />}
            </button>
          )}
          {active && (
            <button className="btn-ghost !p-2 hover:!text-bad" onClick={() => act("stop")} disabled={!!busy} title="停止">
              {busy === "stop" ? <Loader2 className={clsx(icon, "animate-spin")} /> : <Square className={icon} />}
            </button>
          )}
          {!active && run.kind !== "manager" && (
            <button className="btn-ghost !p-2" onClick={() => act("retry")} disabled={!!busy} title="用相同的要求重新运行">
              <RotateCcw className={icon} />
            </button>
          )}
        </div>
      </div>
      {steps && (
        <div className="mt-1.5">
          <ProgressBar run={run} />
        </div>
      )}
    </div>
  );
}

/** Everything a task did: commands, outputs, thinking, file changes. */
export function ProcessSheet() {
  const runId = useStore((s) => s.processRun);
  const run = useRun(runId ?? "");
  const end = useRef<HTMLDivElement>(null);
  const active = run && isActive(run.status);
  const count = run?.steps.reduce((n, s) => n + (s.items?.length ?? 0), 0) ?? 0;
  // A running task opens at its latest step.
  useEffect(() => {
    if (active) end.current?.scrollIntoView({ block: "end" });
  }, [runId, active ? count : 0]);
  if (!run) return null;
  const st = run.steps[0];
  return (
    <Sheet
      wide
      title={
        <span className="flex min-w-0 items-center gap-2">
          <ListTree className="h-4 w-4 shrink-0 text-info" />
          <span className="truncate">执行过程</span>
          <StatusBadge status={run.status} />
        </span>
      }
      onClose={closeProcess}
      footer={<RunControls run={run} />}
    >
      <div className="space-y-2 px-3 py-3 text-[14px] leading-relaxed md:px-4">
        {run.kind === "workflow" ? (
          run.steps.map((s) => <StepCard key={s.id} run={run} step={s} />)
        ) : (st?.items ?? []).length === 0 ? (
          <div className="py-6 text-center text-[13px] text-faint">{active ? "正在启动…" : "没有执行记录"}</div>
        ) : (
          (st.items ?? []).map((it) => <ItemView key={it.id} item={it} />)
        )}
        {active && (
          <div className="flex items-center gap-2 pt-1 text-[12.5px] text-info">
            <Loader2 className="h-3.5 w-3.5 animate-spin" /> {nowText(run)}
          </div>
        )}
        <div ref={end} />
      </div>
    </Sheet>
  );
}

/** In the conversation: one line standing for a task's execution. */
export function ProcessLine({ run }: { run: Run }) {
  const active = isActive(run.status);
  return (
    <button
      onClick={() => openProcess(run.id)}
      className="my-1 flex w-full max-w-full items-center gap-2 rounded-lg border border-line bg-panel px-3 py-1.5 text-left text-[12.5px] text-muted hover:border-faint"
    >
      {active ? (
        <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin text-info" />
      ) : (
        <StatusIcon status={run.status} className="h-3.5 w-3.5 shrink-0" />
      )}
      <span className="min-w-0 flex-1 truncate">
        {active ? (
          <>
            <span className="text-fg">后台执行中</span> · {nowText(run)}
          </>
        ) : (
          <>
            <span className="text-fg">执行过程</span>
            {summary(run) && <> · {summary(run)}</>}
          </>
        )}
      </span>
      <ChevronRight className="h-3.5 w-3.5 shrink-0 text-faint" />
    </button>
  );
}

/** A run that did not complete: why. */
export function OutcomeLine({ run }: { run: Run }) {
  if (run.status === "failed")
    return (
      <div className="my-1 rounded-lg border border-bad/30 bg-bad/5 px-3 py-2 text-[13px] text-bad">
        <span className="font-medium">失败</span>
        {run.error && <span className="whitespace-pre-wrap opacity-90">：{run.error}</span>}
      </div>
    );
  if (run.status === "stopped") return <div className="my-1 text-[12.5px] text-faint">已停止</div>;
  return null;
}

/** Marks a reply as the manager's. */
export function ManagerTag() {
  return (
    <div className="mb-0.5 flex items-center gap-1 text-[11.5px] font-medium text-info">
      <Bot className="h-3.5 w-3.5" /> 管家
    </div>
  );
}

export function ManagerBusy({ run }: { run: Run }) {
  return (
    <div className="flex items-center gap-1.5 py-1 text-[12.5px] text-faint">
      <Loader2 className="h-3.5 w-3.5 animate-spin" /> {run.activity && run.activity !== "思考中" ? run.activity : "正在看任务…"}
    </div>
  );
}

const actionText: Record<string, string> = {
  send_to_task: "已转告 Codex",
  pause_task: "已暂停任务",
  resume_task: "已继续任务",
  stop_task: "已停止任务",
};

/** Something the manager did to the task. */
export function ActionChip({ item }: { item: Item }) {
  const [open, setOpen] = useState(false);
  let message = "";
  try {
    const a = (typeof item.arguments === "string" ? JSON.parse(item.arguments) : item.arguments) as { message?: string } | undefined;
    message = a?.message ?? "";
  } catch {
    /* not JSON */
  }
  const failed = item.status === "failed";
  return (
    <button
      type="button"
      onClick={() => setOpen(!open)}
      className={clsx("my-1 flex w-full items-start gap-1.5 rounded-lg px-2.5 py-1.5 text-left text-[12.5px]", failed ? "bg-bad/5 text-bad" : "bg-info/5 text-muted")}
    >
      {item.status === "running" ? (
        <Loader2 className="mt-0.5 h-3.5 w-3.5 shrink-0 animate-spin" />
      ) : (
        <StatusIcon status={failed ? "failed" : "completed"} className="mt-0.5 h-3.5 w-3.5 shrink-0" />
      )}
      <span className={clsx("min-w-0 break-words", !open && "line-clamp-2")}>
        <span className="font-medium text-fg">{failed ? "没能" + (actionText[item.tool ?? ""] ?? item.tool).replace("已", "") : actionText[item.tool ?? ""] ?? item.tool}</span>
        {message && <span>：{message}</span>}
      </span>
    </button>
  );
}

/** Before a result that arrives after some conversation: says whose result it is. */
export function TaskDoneLine({ run }: { run: Run }) {
  const text = run.status === "completed" ? "任务完成" : run.status === "failed" ? "任务失败" : "任务已停止";
  return (
    <button onClick={() => openProcess(run.id)} className="mb-1 mt-2 flex items-center gap-1.5 text-[12px] font-medium text-muted hover:text-fg">
      <StatusIcon status={run.status} className="h-3.5 w-3.5" /> {text}
      {summary(run) && <span className="font-normal text-faint">· {summary(run)}</span>}
    </button>
  );
}
