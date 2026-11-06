import { useEffect, useMemo, useState } from "react";
import clsx from "clsx";
import {
  Bot,
  ChevronRight,
  FileCode2,
  Loader2,
  Pause,
  Play,
  RotateCcw,
  ScrollText,
  Square,
  Terminal,
  Workflow as WorkflowIcon,
  GitBranch,
  Cpu,
} from "lucide-react";
import type { Run, Step } from "../types";
import { isActive } from "../types";
import { useStore, control, openLogs, loadRun } from "../store";
import { StatusIcon, StatusBadge, Elapsed, fmtDuration, between, shortId, fmtTokens, statusLabel } from "../lib/format";
import { ItemView, DiffView } from "./ItemView";
import { Markdown } from "./Markdown";

const typeMeta: Record<string, { label: string; icon: JSX.Element }> = {
  agent: { label: "Agent", icon: <Bot className="h-3.5 w-3.5" /> },
  shell: { label: "Shell", icon: <Terminal className="h-3.5 w-3.5" /> },
  workflow: { label: "子 Workflow", icon: <WorkflowIcon className="h-3.5 w-3.5" /> },
};

const ifLabel: Record<string, string> = { success: "", failure: "仅依赖失败时", always: "总是执行" };

export function useRun(runId: string): Run | undefined {
  return useStore((s) => s.runs[runId]);
}

/** Pause / resume / stop / retry / logs buttons for a run. */
export function RunControls({ run, size = "sm" }: { run: Run; size?: "sm" | "xs" }) {
  const active = isActive(run.status);
  const cls = size === "xs" ? "btn-ghost !px-1.5 !py-0.5 text-[12px]" : "btn !py-1";
  const [busy, setBusy] = useState<string | null>(null);
  const act = async (a: "pause" | "resume" | "stop" | "retry") => {
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
      {!active && !run.parentRunId && (
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

function layersOf(steps: Step[]): Step[][] {
  const byId = new Map(steps.map((s) => [s.id, s]));
  const level = new Map<string, number>();
  const depth = (s: Step, seen = new Set<string>()): number => {
    if (level.has(s.id)) return level.get(s.id)!;
    if (seen.has(s.id)) return 0;
    seen.add(s.id);
    let l = 0;
    for (const d of s.dependsOn ?? []) {
      const ds = byId.get(d);
      if (ds) l = Math.max(l, depth(ds, seen) + 1);
    }
    level.set(s.id, l);
    return l;
  };
  steps.forEach((s) => depth(s));
  const out: Step[][] = [];
  for (const s of steps) (out[level.get(s.id)!] ??= []).push(s);
  return out;
}

/** A run executed by one orchestrating Codex agent (free-form workflow). */
export const orchestrated = (run: Run) => run.steps.length === 1 && !!run.steps[0].orchestrate;

/** Steps Codex reported for a single-agent run (chat or free-form). */
export const reportedPlan = (run: Run) => (run.steps.length === 1 ? run.steps[0].plan ?? [] : []);

/** Step-like entries for progress display: real steps, or what the orchestrator reported. */
export function progressUnits(run: Run): { title: string; status: string }[] {
  if (orchestrated(run) || reportedPlan(run).length > 0) {
    const st = run.steps[0];
    const plan = st.plan ?? [];
    if (!plan.length) return [{ title: st.title, status: st.status }];
    // Once the run is over, steps still marked running/pending did not finish.
    const over = !["running", "paused", "pending", "queued"].includes(st.status);
    return plan.map((p) => ({
      title: p.step,
      status: over && (p.status === "running" || p.status === "pending") ? (st.status === "completed" ? "completed" : "stopped") : p.status,
    }));
  }
  return run.steps.map((s) => ({ title: s.title, status: s.status }));
}

export function stepTitle(run: Run, id: string) {
  return run.steps.find((s) => s.id === id)?.title ?? id;
}

/** Workflow DAG overview: dependency layers left to right. */
export function Pipeline({ run }: { run: Run }) {
  const layers = useMemo(() => layersOf(run.steps), [run.steps]);
  if (orchestrated(run)) {
    const units = progressUnits(run);
    if (!run.steps[0].plan?.length) {
      return (
        <div className="text-[12px] text-faint">
          {run.steps[0].status === "running" ? "Codex 正在阅读文档、拆分步骤…" : "Codex 没有汇报步骤"}
        </div>
      );
    }
    return (
      <div className="flex flex-wrap items-center gap-1.5">
        {units.map((u, i) => (
          <div key={i} className="flex items-center gap-1.5">
            {i > 0 && <ChevronRight className="h-3.5 w-3.5 shrink-0 text-faint" />}
            <span
              className={clsx(
                "flex max-w-[200px] items-center gap-1.5 rounded-md border px-2 py-1 text-[12px]",
                u.status === "running" && "border-info/50 bg-info/5",
                u.status === "failed" && "border-bad/40 bg-bad/5",
                u.status === "completed" && "border-ok/30",
                !["running", "failed", "completed"].includes(u.status) && "border-line",
              )}
            >
              <StatusIcon status={u.status} className="h-3 w-3" />
              <span className="truncate">{u.title}</span>
            </span>
          </div>
        ))}
      </div>
    );
  }
  const jump = (stepId: string) =>
    document.getElementById(`step-${run.id}-${stepId}`)?.scrollIntoView({ behavior: "smooth", block: "center" });
  return (
    <div className="flex items-stretch gap-1.5 overflow-x-auto pb-1">
      {layers.map((layer, i) => (
        <div key={i} className="flex items-center gap-1.5">
          {i > 0 && <ChevronRight className="h-3.5 w-3.5 shrink-0 text-faint" />}
          <div className="flex flex-col gap-1">
            {layer.map((s) => (
              <button
                key={s.id}
                onClick={() => jump(s.id)}
                title={`${s.title}（${statusLabel[s.status]}）${s.dependsOn?.length ? `\n等待：${s.dependsOn.map((d) => stepTitle(run, d)).join("、")}` : ""}`}
                className={clsx(
                  "flex max-w-[200px] items-center gap-1.5 rounded-md border px-2 py-1 text-[12px]",
                  s.status === "running" && "border-info/50 bg-info/5",
                  s.status === "failed" && "border-bad/40 bg-bad/5",
                  s.status === "completed" && "border-ok/30",
                  s.status === "paused" && "border-warn/40 bg-warn/5",
                  !["running", "failed", "completed", "paused"].includes(s.status) && "border-line",
                )}
              >
                <StatusIcon status={s.status} className="h-3 w-3" />
                <span className="truncate">{s.title}</span>
              </button>
            ))}
          </div>
        </div>
      ))}
    </div>
  );
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

export function RunHeader({ run }: { run: Run }) {
  const [showSource, setShowSource] = useState(false);
  return (
    <div className="mb-3 rounded-xl border border-line bg-panel p-3">
      <div className="flex flex-wrap items-center gap-2">
        <WorkflowIcon className="h-4 w-4 text-info" />
        <span className="font-medium">{run.title}</span>
        {run.workflowFile && <span className="chip font-mono">{run.workflowFile}</span>}
        <StatusBadge status={run.status} />
        <span className="text-[12px] text-faint">
          <Elapsed start={run.startedAt} end={run.endedAt} running={isActive(run.status)} />
        </span>
        <div className="ml-auto">
          <RunControls run={run} size="xs" />
        </div>
      </div>
      {run.workflow?.description && <div className="mt-1 text-[12.5px] text-muted">{run.workflow.description}</div>}
      <div className="mt-2">
        <ProgressBar run={run} />
      </div>
      <div className="mt-2">
        <Pipeline run={run} />
      </div>
      <div className="mt-1 flex flex-wrap gap-x-4 gap-y-1 text-[11.5px] text-faint">
        <span>
          工作目录 <code className="font-mono text-muted">{run.cwd}</code>
        </span>
        {run.sandbox && <span>沙箱 {run.sandbox}</span>}
        {run.model && <span>模型 {run.model}</span>}
        {run.vars && Object.keys(run.vars).length > 0 && (
          <span>
            变量{" "}
            {Object.entries(run.vars).map(([k, v]) => (
              <code key={k} className="mr-1 font-mono text-muted">
                {k}={v.length > 30 ? v.slice(0, 30) + "…" : v}
              </code>
            ))}
          </span>
        )}
        {run.source && (
          <button className="underline decoration-dotted hover:text-fg" onClick={() => setShowSource(!showSource)}>
            {showSource ? "隐藏" : "查看"} workflow 源码
          </button>
        )}
      </div>
      {showSource && run.source && (
        <pre className="term mt-2 max-h-80 overflow-auto rounded-lg border border-line bg-bg p-3">{run.source}</pre>
      )}
    </div>
  );
}

function StepDetails({ run, step }: { run: Run; step: Step }) {
  const [showPrompt, setShowPrompt] = useState(false);
  const [showDiff, setShowDiff] = useState(false);
  const [showProc, setShowProc] = useState(false);
  const usage = step.usage?.total;
  return (
    <div className="flex flex-wrap gap-x-3 gap-y-1 text-[11.5px] text-faint">
      <span>
        cwd <code className="font-mono text-muted">{step.cwd}</code>
      </span>
      {step.threadId && <span>Codex 线程 {shortId(step.threadId)}</span>}
      {usage?.totalTokens ? (
        <span title={`输入 ${usage.inputTokens}（缓存 ${usage.cachedInputTokens}）· 输出 ${usage.outputTokens}`}>
          tokens {fmtTokens(usage.totalTokens)}
        </span>
      ) : null}
      {step.prompt && step.type !== "shell" && (
        <button className="underline decoration-dotted hover:text-fg" onClick={() => setShowPrompt(!showPrompt)}>
          {showPrompt ? "隐藏" : "查看"}提示词
        </button>
      )}
      {step.processes && step.processes.length > 0 && (
        <button className="underline decoration-dotted hover:text-fg" onClick={() => setShowProc(!showProc)}>
          进程 ×{step.processes.length}
        </button>
      )}
      {step.diff && (
        <button className="underline decoration-dotted hover:text-fg" onClick={() => setShowDiff(!showDiff)}>
          {showDiff ? "隐藏" : "查看"}本步骤 diff
        </button>
      )}
      <button className="underline decoration-dotted hover:text-fg" onClick={() => openLogs(run.id, step.id)}>
        步骤日志
      </button>
      {showPrompt && (
        <pre className="term mt-1 max-h-72 w-full overflow-auto rounded-lg border border-line bg-panel p-2 text-muted">{step.prompt}</pre>
      )}
      {showProc && (
        <div className="mt-1 w-full space-y-1">
          {step.processes!.map((p, i) => (
            <div key={i} className="rounded-md border border-line bg-panel px-2 py-1 font-mono text-[11.5px] text-muted">
              <div className="truncate text-fg">$ {p.argv.join(" ")}</div>
              <div>
                pid {p.pid} · cwd {p.cwd} · {p.endedAt ? `退出码 ${p.exitCode ?? "?"} · ${fmtDuration(between(p.startedAt, p.endedAt))}` : "运行中"}
                {p.error ? ` · ${p.error}` : ""}
              </div>
            </div>
          ))}
        </div>
      )}
      {showDiff && step.diff && (
        <div className="mt-1 w-full overflow-hidden rounded-lg border border-line">
          <DiffView diff={step.diff} />
        </div>
      )}
    </div>
  );
}

export function StepCard({ run, step, nested }: { run: Run; step: Step; nested?: boolean }) {
  const active = step.status === "running" || step.status === "paused";
  const [open, setOpen] = useState<boolean | null>(null);
  const isOpen = open ?? (active || step.status === "failed");
  const meta = typeMeta[step.type] ?? typeMeta.agent;
  const items = step.items ?? [];
  return (
    <div
      id={`step-${run.id}-${step.id}`}
      className={clsx(
        "rounded-xl border bg-bg transition-colors",
        step.status === "running" ? "border-info/40" : step.status === "failed" ? "border-bad/40" : "border-line",
        nested ? "" : "shadow-sm",
      )}
    >
      <button
        onClick={() => setOpen(!isOpen)}
        className="flex w-full items-center gap-2 rounded-xl px-2.5 py-2.5 text-left hover:bg-panel md:px-3"
      >
        <ChevronRight className={clsx("h-3.5 w-3.5 shrink-0 text-faint transition-transform", isOpen && "rotate-90")} />
        <StatusIcon status={step.status} />
        {run.steps.length > 1 && <span className="shrink-0 text-[12px] tabular-nums text-faint">{run.steps.indexOf(step) + 1}.</span>}
        <span className="min-w-0 truncate font-medium">{step.title}</span>
        <span className="chip hidden shrink-0 sm:inline-flex">
          {meta.icon}
          {meta.label}
        </span>
        {step.dependsOn?.length > 0 && (
          <span className="hidden shrink-0 items-center gap-0.5 text-[11px] text-faint md:inline-flex" title="依赖">
            <GitBranch className="h-3 w-3" />
            {step.dependsOn.map((d) => stepTitle(run, d)).join("、")}
          </span>
        )}
        {ifLabel[step.if] && <span className="chip hidden shrink-0 sm:inline-flex">{ifLabel[step.if]}</span>}
        {step.maxAttempts > 1 && (
          <span className="chip shrink-0">
            尝试 {Math.max(step.attempt, 1)}/{step.maxAttempts}
          </span>
        )}
        <span className="ml-auto flex min-w-0 items-center gap-2 text-[12px] text-muted">
          {active && step.activity && (
            <span className={clsx("hidden truncate md:inline", step.status === "paused" ? "text-warn" : "text-info")}>{step.activity}</span>
          )}
          <span className="shrink-0 text-faint">
            <Elapsed start={step.startedAt} end={step.endedAt} running={active} />
          </span>
        </span>
      </button>
      {isOpen && (
        <div className="space-y-2 border-t border-line px-3 py-3">
          <StepDetails run={run} step={step} />
          {step.type === "shell" && step.run && items.length === 0 && (
            <pre className="term rounded-lg bg-sunken/60 px-3 py-2">{step.run}</pre>
          )}
          {items.map((it) => (
            <ItemView key={it.id} item={it} />
          ))}
          {step.status === "pending" && (
            <div className="text-[12.5px] text-faint">
              等待{step.dependsOn?.length ? `「${step.dependsOn.map((d) => stepTitle(run, d)).join("」「")}」完成` : "开始"}
            </div>
          )}
          {active && (
            <div className="flex items-center gap-2 text-[12.5px]">
              {step.status === "running" ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin text-info" />
              ) : (
                <Pause className="h-3.5 w-3.5 text-warn" />
              )}
              <span className={step.status === "running" ? "shimmer-text" : "text-warn"}>
                {step.activity || (step.status === "paused" ? "已暂停" : "执行中")}
              </span>
            </div>
          )}
          {step.status === "completed" && step.output && step.type !== "agent" && (
            <div className="rounded-lg border border-line bg-panel px-3 py-2">
              <div className="mb-1 text-[11px] font-medium text-faint">步骤输出</div>
              {step.type === "shell" ? <pre className="term">{step.output}</pre> : <Markdown text={step.output} />}
            </div>
          )}
          {step.error && step.status !== "completed" && (
            <div className={clsx("rounded-lg px-3 py-2 text-[12.5px]", step.status === "failed" ? "bg-bad/10 text-bad" : "bg-panel text-muted")}>
              {step.error}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

/** A sub-workflow run rendered inside its parent step. */
export function ChildRunView({ runId }: { runId: string }) {
  const run = useRun(runId);
  useEffect(() => {
    if (!run || run.steps.some((s) => s.items === undefined)) void loadRun(runId).catch(() => undefined);
  }, [runId, !!run]);
  if (!run)
    return (
      <div className="flex items-center gap-2 px-2 py-1 text-[12px] text-faint">
        <Loader2 className="h-3 w-3 animate-spin" /> 加载子任务…
      </div>
    );
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center gap-2 px-1 text-[12px]">
        <StatusBadge status={run.status} />
        <span className="text-muted">{run.title}</span>
        <div className="min-w-[120px] flex-1">
          <ProgressBar run={run} />
        </div>
        <RunControls run={run} size="xs" />
      </div>
      {run.steps.map((st) => (
        <StepCard key={st.id} run={run} step={st} nested />
      ))}
      {run.output && run.status === "completed" && (
        <div className="rounded-lg border border-line bg-panel px-3 py-2">
          <div className="mb-1 text-[11px] font-medium text-faint">子 workflow 结果</div>
          <Markdown text={run.output} />
        </div>
      )}
    </div>
  );
}

/** Live "what is the agent doing" line shown at the bottom of an active run. */
export function ActivityLine({ run }: { run: Run }) {
  const text =
    run.status === "queued"
      ? "排队中，等待空闲执行槽位…"
      : run.status === "paused"
        ? "已暂停，点击“继续”恢复执行"
        : run.activity || "执行中…";
  return (
    <div className="mt-2 flex flex-wrap items-center gap-2 rounded-xl border border-line bg-panel px-3 py-2 text-[13px]">
      {run.status === "paused" ? (
        <Pause className="h-4 w-4 text-warn" />
      ) : (
        <Loader2 className={clsx("h-4 w-4 animate-spin", run.status === "queued" ? "text-faint" : "text-info")} />
      )}
      <span className={clsx("min-w-0 flex-1 truncate", run.status === "running" ? "shimmer-text font-medium" : "text-muted")}>
        {text}
      </span>
      <span className="text-[12px] text-faint">
        <Elapsed start={run.startedAt ?? run.createdAt} running />
      </span>
      <RunControls run={run} size="xs" />
    </div>
  );
}

/** Final result / error / footer once a run has finished. */
export function ResultBlock({ run }: { run: Run }) {
  const tokens = run.steps.reduce((n, s) => n + (s.usage?.total?.totalTokens ?? 0), 0);
  const showOutput = run.kind === "workflow" && run.status === "completed" && run.output;
  return (
    <div className="mt-2 space-y-2">
      {showOutput && (
        <div className="rounded-xl border border-ok/30 bg-ok/5 px-4 py-3">
          <div className="mb-1 flex items-center gap-1.5 text-[12px] font-medium text-ok">
            <FileCode2 className="h-3.5 w-3.5" /> 最终结果
          </div>
          <Markdown text={run.output!} />
        </div>
      )}
      {run.status === "failed" && (
        <div className="rounded-xl border border-bad/30 bg-bad/5 px-4 py-2.5 text-[13px] text-bad">
          <div className="font-medium">任务失败</div>
          <div className="whitespace-pre-wrap opacity-90">{run.error}</div>
        </div>
      )}
      {run.status === "stopped" && (
        <div className="rounded-xl border border-line bg-panel px-4 py-2 text-[13px] text-muted">任务已被停止</div>
      )}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11.5px] text-faint">
        <span className="inline-flex items-center gap-1">
          <StatusIcon status={run.status} className="h-3 w-3" />
          {statusLabel[run.status]}
        </span>
        {run.startedAt && <span>耗时 {fmtDuration(between(run.startedAt, run.endedAt))}</span>}
        {tokens > 0 && (
          <span className="inline-flex items-center gap-1">
            <Cpu className="h-3 w-3" />
            {fmtTokens(tokens)} tokens
          </span>
        )}
        <RunControls run={run} size="xs" />
      </div>
    </div>
  );
}
