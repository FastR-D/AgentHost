import { useState } from "react";
import clsx from "clsx";
import { Bot, ChevronRight, Loader2, Pause, Play, RotateCcw, Square, StepForward, Users } from "lucide-react";
import type { Item, Run, Step, SubAgent } from "../types";
import { isActive } from "../types";
import { control } from "../store";
import { Elapsed, StatusIcon, between, fmtDuration, fmtTokens, statusLabel } from "../lib/format";
import { ItemView } from "./ItemView";
import { finalItem } from "./messages";
import { isMultiParallel, parallelLabel } from "./Parallel";
import { ProgressBar, RunControls, canContinue, progressUnits } from "./RunControls";

/** Tokens of a run, sub-agents included. */
const runTokens = (run: Run) =>
  run.steps.reduce((n, s) => n + (s.usage?.total?.totalTokens ?? 0) + (s.agents ?? []).reduce((m, a) => m + (a.tokens ?? 0), 0), 0);

const agentsOf = (run: Run) => run.steps.flatMap((s) => s.agents ?? []);

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
  const agents = agentsOf(run).length;
  if (agents) parts.push(`${agents} 个子 Codex`);
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

/** The session's task, pinned under the header: what it is doing, its
 *  progress and the controls. Tap to jump to its execution in the conversation. */
export function TaskBar({ run }: { run: Run }) {
  const active = isActive(run.status);
  const [busy, setBusy] = useState<string | null>(null);
  const tokens = runTokens(run);
  const act = async (a: "pause" | "resume" | "stop" | "retry" | "continue") => {
    setBusy(a);
    await control(run.id, a);
    setBusy(null);
  };
  const icon = "h-3.5 w-3.5";
  const spin = <Loader2 className={clsx(icon, "animate-spin")} />;
  const jump = () => document.getElementById(`proc-${run.id}`)?.scrollIntoView({ block: "center", behavior: "smooth" });
  return (
    <div className={clsx("border-b border-line px-3 py-2 md:px-4", active ? "bg-info/5" : run.status === "interrupted" ? "bg-warn/5" : "bg-panel")}>
      <div className="flex items-center gap-2">
        <button className="flex min-w-0 flex-1 items-center gap-2 text-left" onClick={jump} title="跳到执行过程">
          {run.status === "running" ? (
            <Loader2 className="h-4 w-4 shrink-0 animate-spin text-info" />
          ) : (
            <StatusIcon status={run.status} className="h-4 w-4 shrink-0" />
          )}
          <span className="min-w-0 flex-1">
            <span className={clsx("block truncate text-[13px]", run.status === "running" ? "shimmer-text font-medium" : "text-fg")}>
              {active ? nowText(run) : run.status === "interrupted" ? "已中断，可以继续" : statusLabel[run.status]}
            </span>
            <span className="block truncate text-[11px] text-faint">
              {run.taskTitle || run.title}
              {" · "}
              {active ? (
                <>
                  <Elapsed start={run.startedAt ?? run.createdAt} running />
                  {isMultiParallel(run.parallel) && <> · {parallelLabel(run.parallel)}</>}
                </>
              ) : (
                [summary(run), tokens ? `${fmtTokens(tokens)} tokens` : ""].filter(Boolean).join(" · ")
              )}
            </span>
          </span>
        </button>
        <div className="flex shrink-0 items-center">
          {active && run.status !== "paused" && (
            <button className="btn-ghost !p-2" onClick={() => act("pause")} disabled={!!busy} title="暂停">
              {busy === "pause" ? spin : <Pause className={icon} />}
            </button>
          )}
          {run.status === "paused" && (
            <button className="btn-ghost !p-2" onClick={() => act("resume")} disabled={!!busy} title="继续">
              {busy === "resume" ? spin : <Play className={icon} />}
            </button>
          )}
          {active && (
            <button className="btn-ghost !p-2 hover:!text-bad" onClick={() => act("stop")} disabled={!!busy} title="停止">
              {busy === "stop" ? spin : <Square className={icon} />}
            </button>
          )}
          {canContinue(run) && (
            <button className="btn !px-2 !py-1 text-[12px]" onClick={() => act("continue")} disabled={!!busy} title="在原来的 Codex 会话上接着做">
              {busy === "continue" ? spin : <StepForward className={icon} />} 继续
            </button>
          )}
          {!active && (
            <button className="btn-ghost !p-2" onClick={() => act("retry")} disabled={!!busy} title="用相同的要求重新运行">
              <RotateCcw className={icon} />
            </button>
          )}
        </div>
      </div>
      {active && progressUnits(run).length > 1 && (
        <div className="mt-1.5">
          <ProgressBar run={run} />
        </div>
      )}
    </div>
  );
}

/** Which process lines are open; survives the conversation being rebuilt. */
const openRuns = new Set<string>();

/** A step's timeline with each sub-Codex folded into one card where it started. */
function Timeline({ step, hide }: { step: Step; hide?: string }) {
  const items = (step.items ?? []).filter((it) => it.id !== hide);
  const agents = step.agents ?? [];
  const known = new Set(agents.map((a) => a.threadId));
  const placed = new Set<string>();
  const out: JSX.Element[] = [];
  for (const it of items) {
    if (it.threadId) continue; // shown in its agent's card
    if (it.kind === "agent" && it.agentThread) {
      const a = agents.find((x) => x.threadId === it.agentThread);
      if (it.tool === "started" && a && !placed.has(a.threadId)) {
        placed.add(a.threadId);
        out.push(<SubAgentCard key={it.id} agent={a} items={items.filter((x) => x.threadId === a.threadId)} />);
      }
      continue;
    }
    if (it.kind === "agent" && it.tool === "wait") continue;
    out.push(<ItemView key={it.id} item={it} />);
  }
  // Sub-agents whose start AgentHost did not see.
  const orphans = [...new Set(items.filter((x) => x.threadId && !known.has(x.threadId)).map((x) => x.threadId!))];
  orphans.forEach((th, i) =>
    out.push(
      <SubAgentCard
        key={th}
        agent={{ threadId: th, name: `子 Codex ${i + 1}`, status: "completed" }}
        items={items.filter((x) => x.threadId === th)}
      />,
    ),
  );
  return <>{out}</>;
}

/** One sub-Codex: what it is doing now; open for its own commands and messages. */
function SubAgentCard({ agent, items }: { agent: SubAgent; items: Item[] }) {
  const [open, setOpen] = useState(false);
  const shown = items.filter((it) => !(it.kind === "agent" && (it.tool === "interacted" || it.tool === "wait")));
  const commands = shown.filter((it) => it.kind === "command").length;
  const running = agent.status === "running";
  const last = [...shown].reverse().find((it) => it.kind === "message" && it.text);
  const sub = running
    ? agent.activity || "思考中"
    : [agent.status === "stopped" ? "已停止" : "已完成", commands ? `${commands} 条命令` : "", agent.tokens ? `${fmtTokens(agent.tokens)} tokens` : ""]
        .filter(Boolean)
        .join(" · ");
  return (
    <div className={clsx("rounded-xl border", running ? "border-info/40 bg-info/5" : "border-line")}>
      <button className="flex w-full items-center gap-2 px-3 py-2 text-left" onClick={() => setOpen(!open)}>
        {running ? (
          <Loader2 className="h-4 w-4 shrink-0 animate-spin text-info" />
        ) : (
          <StatusIcon status={agent.status === "stopped" ? "stopped" : "completed"} className="h-4 w-4 shrink-0" />
        )}
        <Users className="h-3.5 w-3.5 shrink-0 text-muted" />
        <span className="min-w-0 flex-1">
          <span className="block truncate font-mono text-[13px] font-medium">{agent.name}</span>
          <span className={clsx("block truncate text-[11.5px]", running ? "text-info" : "text-faint")}>{sub}</span>
        </span>
        <ChevronRight className={clsx("h-4 w-4 shrink-0 text-faint transition-transform", open && "rotate-90")} />
      </button>
      {!open && !running && last?.text && <div className="line-clamp-2 px-3 pb-2 pl-[52px] text-[12.5px] text-muted">{last.text}</div>}
      {open && (
        <div className="space-y-2 border-t border-line px-3 py-2">
          {shown.length === 0 ? <div className="text-[12.5px] text-faint">还没有动作</div> : shown.map((it) => <ItemView key={it.id} item={it} />)}
        </div>
      )}
    </div>
  );
}

/** In the conversation: one line standing for a task's execution; tap it to
 *  unfold everything it did right here. State and controls live on the card. */
export function ProcessLine({ run }: { run: Run }) {
  const active = isActive(run.status);
  const [open, setOpen] = useState(() => openRuns.has(run.id));
  const toggle = () => {
    if (open) openRuns.delete(run.id);
    else openRuns.add(run.id);
    setOpen(!open);
  };
  const st = run.steps[0];
  return (
    <div id={`proc-${run.id}`} className="my-1 w-full max-w-full rounded-lg border border-line bg-panel">
      <button onClick={toggle} className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-[12.5px] text-muted" title={open ? "收起执行过程" : "展开执行过程"}>
        {active ? <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin text-info" /> : <StatusIcon status={run.status} className="h-3.5 w-3.5 shrink-0" />}
        <span className="min-w-0 flex-1 truncate">
          <span className="text-fg">执行过程</span>
          {summary(run) && <> · {summary(run)}</>}
        </span>
        <ChevronRight className={clsx("h-3.5 w-3.5 shrink-0 text-faint transition-transform", open && "rotate-90")} />
      </button>
      {open && (
        <div className="space-y-2 border-t border-line px-3 py-2 text-[14px] leading-relaxed">
          {(st?.items ?? []).length === 0 ? (
            <div className="py-3 text-center text-[13px] text-faint">{active ? "正在启动…" : "没有执行记录"}</div>
          ) : (
            // The final answer is shown right below in the conversation.
            <Timeline step={st} hide={active ? undefined : finalItem(run)?.id} />
          )}
          {active && (
            <div className="flex items-center gap-2 pt-1 text-[12.5px] text-info">
              <Loader2 className="h-3.5 w-3.5 animate-spin" /> {nowText(run)}
            </div>
          )}
          <div className="border-t border-line pt-2">
            <RunControls run={run} size="xs" />
          </div>
        </div>
      )}
    </div>
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
  if (run.status === "interrupted")
    return (
      <div className="my-1 rounded-lg border border-warn/30 bg-warn/5 px-3 py-2 text-[13px] text-warn">
        <span className="font-medium">已中断</span>
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
    <div className="mb-1 mt-2 flex items-center gap-1.5 text-[12px] font-medium text-muted">
      <StatusIcon status={run.status} className="h-3.5 w-3.5" /> {text}
      {summary(run) && <span className="font-normal text-faint">· {summary(run)}</span>}
    </div>
  );
}
