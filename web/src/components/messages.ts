import type { ThreadMessageLike } from "@assistant-ui/react";
import type { Run } from "../types";
import { isActive } from "../types";

export interface MsgCustom {
  runId: string;
  kind: Run["kind"];
}

type Part = Exclude<ThreadMessageLike["content"], string>[number];

const data = (name: string, d: Record<string, unknown>) => ({ type: `data-${name}`, data: d }) as Part;

/** The Codex reply to show in the conversation: its final answer. */
export function finalText(run: Run): string {
  const items = (run.steps[0]?.items ?? []).filter((it) => it.kind === "message" && !it.threadId && it.text);
  const final = [...items].reverse().find((it) => it.phase === "final_answer") ?? items[items.length - 1];
  return final?.text ?? "";
}

/** Manager tools the user should see as actions. */
const ACTIONS = new Set(["send_to_task", "pause_task", "resume_task", "stop_task"]);

function managerParts(run: Run): Part[] {
  const parts: Part[] = [data("manager", { runId: run.id })];
  const st = run.steps[0];
  for (const it of st?.items ?? []) {
    if (it.kind === "tool" && it.tool && ACTIONS.has(it.tool)) parts.push(data("action", { runId: run.id, stepId: st.id, itemId: it.id }));
  }
  const text = finalText(run);
  if (text) parts.push({ type: "text", text });
  if (isActive(run.status)) parts.push(data("manager-busy", { runId: run.id }));
  else if (run.status !== "completed") parts.push(data("outcome", { runId: run.id }));
  return parts;
}

function userContent(run: Run): ThreadMessageLike["content"] {
  if (run.kind === "workflow") return [data("workflow-request", { runId: run.id })];
  if (run.task || run.taskFile) return [data("task-request", { runId: run.id })];
  return [{ type: "text", text: run.prompt }];
}

const done = { type: "complete", reason: "stop" } as const;

/** Converts a session's runs into one conversation. A task's execution is
 *  one compact line (details open in a sheet); its result follows. While a
 *  task runs, the user's questions and the manager's answers come in between,
 *  and the result appears when the task finishes. */
export function buildMessages(runs: Run[]): ThreadMessageLike[] {
  const entries: { at: number; seq: number; msg: ThreadMessageLike }[] = [];
  const push = (at: number, msg: ThreadMessageLike) => entries.push({ at, seq: entries.length, msg });
  const managers = runs.filter((r) => r.kind === "manager");

  for (const run of runs) {
    const created = Date.parse(run.createdAt);
    const custom: MsgCustom = { runId: run.id, kind: run.kind };
    const meta = { custom: { ...custom } };
    push(created, { id: `${run.id}:user`, role: "user", createdAt: new Date(run.createdAt), content: userContent(run), metadata: meta });

    if (run.kind === "manager") {
      push(created, { id: `${run.id}:assistant`, role: "assistant", content: managerParts(run), status: isActive(run.status) ? { type: "running" } : done, metadata: meta });
      continue;
    }
    if (run.kind === "workflow") {
      const parts: Part[] = [data("run-header", { runId: run.id })];
      for (const st of run.steps) parts.push(data("step", { runId: run.id, stepId: st.id }));
      parts.push(isActive(run.status) ? data("activity", { runId: run.id }) : data("result", { runId: run.id }));
      push(created, { id: `${run.id}:assistant`, role: "assistant", content: parts, status: done, metadata: meta });
      continue;
    }

    const process = data("process", { runId: run.id });
    const result: Part[] = [];
    if (!isActive(run.status)) {
      const text = finalText(run);
      if (text) result.push({ type: "text", text });
      if (run.status !== "completed") result.push(data("outcome", { runId: run.id }));
    }
    const end = run.endedAt ? Date.parse(run.endedAt) : Infinity;
    const talkedDuring = managers.some((m) => m.target === run.id || (Date.parse(m.createdAt) > created && Date.parse(m.createdAt) < end));
    if (talkedDuring && result.length) {
      // The result comes after the conversation that happened while it ran.
      push(created, { id: `${run.id}:assistant`, role: "assistant", content: [process], status: done, metadata: meta });
      push(end, { id: `${run.id}:result`, role: "assistant", content: [data("task-done", { runId: run.id }), ...result], status: done, metadata: meta });
    } else {
      push(created, { id: `${run.id}:assistant`, role: "assistant", content: [process, ...result], status: done, metadata: meta });
    }
  }
  entries.sort((a, b) => a.at - b.at || a.seq - b.seq);
  return entries.map((e) => e.msg);
}
