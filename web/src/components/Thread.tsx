import { useState, type FC } from "react";
import clsx from "clsx";
import {
  AuiIf,
  ComposerPrimitive,
  MessagePrimitive,
  ThreadPrimitive,
  useAuiState,
  type DataMessagePartProps,
  type ToolCallMessagePartProps,
} from "@assistant-ui/react";
import { MarkdownTextPrimitive } from "@assistant-ui/react-markdown";
import remarkGfm from "remark-gfm";
import { ArrowDown, ArrowUp, Bot, ClipboardList, FileText, Square, Workflow as WorkflowIcon, ChevronRight } from "lucide-react";
import { useStore, navigate } from "../store";
import type { Item } from "../types";
import { ItemView, ReasoningView } from "./ItemView";
import { Markdown, fixCjkEmphasis, mdComponents } from "./Markdown";
import { ActivityLine, ResultBlock, RunHeader, StepCard, useRun } from "./StepCard";
import { ActionChip, ManagerBusy, ManagerTag, OutcomeLine, ProcessLine, TaskDoneLine } from "./TaskBar";
import type { MsgCustom } from "./messages";

// ---------------------------------------------------------------- part renderers

function useItem(runId: string, stepId: string, itemId: string): Item | undefined {
  return useStore((s) => s.runs[runId]?.steps.find((st) => st.id === stepId)?.items?.find((it) => it.id === itemId));
}

const MarkdownText: FC = () => (
  <MarkdownTextPrimitive remarkPlugins={[remarkGfm]} className="md" smooth components={mdComponents} preprocess={fixCjkEmphasis} />
);

/** Every Codex item that is not plain text is rendered as an assistant-ui tool call. */
const ItemToolUI: FC<ToolCallMessagePartProps<{ runId: string; stepId: string; itemId: string }>> = ({ args }) => {
  const item = useItem(args.runId, args.stepId, args.itemId);
  if (!item) return null;
  return (
    <div className="my-1.5">
      <ItemView item={item} />
    </div>
  );
};

type Ref = { runId: string; stepId?: string; itemId?: string };

const ReasoningPart: FC<DataMessagePartProps<Ref>> = ({ data }) => {
  const item = useItem(data.runId, data.stepId!, data.itemId!);
  if (!item) return null;
  return (
    <div className="my-1.5">
      <ReasoningView text={item.text} running={item.status === "running"} startedAt={item.startedAt} endedAt={item.endedAt} />
    </div>
  );
};

const RunHeaderPart: FC<DataMessagePartProps<Ref>> = ({ data }) => {
  const run = useRun(data.runId);
  return run ? <RunHeader run={run} /> : null;
};

const StepPart: FC<DataMessagePartProps<Ref>> = ({ data }) => {
  const run = useRun(data.runId);
  const step = run?.steps.find((s) => s.id === data.stepId);
  if (!run || !step) return null;
  return (
    <div className="mb-2">
      <StepCard run={run} step={step} />
    </div>
  );
};

const ActivityPart: FC<DataMessagePartProps<Ref>> = ({ data }) => {
  const run = useRun(data.runId);
  return run ? <ActivityLine run={run} /> : null;
};

const ResultPart: FC<DataMessagePartProps<Ref>> = ({ data }) => {
  const run = useRun(data.runId);
  return run ? <ResultBlock run={run} /> : null;
};

const ProcessPart: FC<DataMessagePartProps<Ref>> = ({ data }) => {
  const run = useRun(data.runId);
  return run ? <ProcessLine run={run} /> : null;
};

const OutcomePart: FC<DataMessagePartProps<Ref>> = ({ data }) => {
  const run = useRun(data.runId);
  return run ? <OutcomeLine run={run} /> : null;
};

const ManagerPart: FC<DataMessagePartProps<Ref>> = () => <ManagerTag />;

const TaskDonePart: FC<DataMessagePartProps<Ref>> = ({ data }) => {
  const run = useRun(data.runId);
  return run ? <TaskDoneLine run={run} /> : null;
};

const ManagerBusyPart: FC<DataMessagePartProps<Ref>> = ({ data }) => {
  const run = useRun(data.runId);
  return run ? <ManagerBusy run={run} /> : null;
};

const ActionPart: FC<DataMessagePartProps<Ref>> = ({ data }) => {
  const item = useItem(data.runId, data.stepId!, data.itemId!);
  return item ? <ActionChip item={item} /> : null;
};

const WorkflowRequestPart: FC<DataMessagePartProps<Ref>> = ({ data }) => {
  const run = useRun(data.runId);
  const [open, setOpen] = useState(false);
  if (!run) return null;
  return (
    <div className="min-w-[260px]">
      <div className="flex items-center gap-2">
        <WorkflowIcon className="h-4 w-4 text-info" />
        <span>
          运行 Workflow <span className="font-medium">「{run.title}」</span>
        </span>
      </div>
      <div className="mt-1 flex flex-wrap gap-1.5 text-[11.5px] text-muted">
        {run.workflowFile ? <span className="chip font-mono">{run.workflowFile}</span> : <span className="chip">临时提交</span>}
        <span className="chip">{run.steps.length} 个步骤</span>
        {run.retryOf && <span className="chip">重新运行</span>}
      </div>
      {run.source && (
        <button onClick={() => setOpen(!open)} className="mt-1 flex items-center gap-1 text-[12px] text-muted hover:text-fg">
          <ChevronRight className={clsx("h-3 w-3 transition-transform", open && "rotate-90")} />
          Markdown 源码
        </button>
      )}
      {open && <pre className="term mt-1 max-h-72 overflow-auto rounded-lg bg-bg p-2 text-[11.5px]">{run.source}</pre>}
    </div>
  );
};

const NOTE_SEP = "\n\n---\n\n补充说明：";

/** The user's side of a task run: which task, what they added, and the full text. */
const TaskRequestPart: FC<DataMessagePartProps<Ref>> = ({ data }) => {
  const run = useRun(data.runId);
  const [open, setOpen] = useState(false);
  if (!run) return null;
  const file = run.taskFile;
  const body = run.note && run.prompt.endsWith(NOTE_SEP + run.note) ? run.prompt.slice(0, -(NOTE_SEP + run.note).length) : run.prompt;
  const go = () => {
    if (run.task) navigate({ page: "tasks", name: run.task });
    else if (file) navigate({ page: "files", node: run.nodeId, path: file.slice(0, file.lastIndexOf("/")) || "/", file });
  };
  return (
    <div className="min-w-[200px]">
      <button
        className="inline-flex max-w-full items-center gap-1.5 rounded-full bg-info/10 px-2.5 py-0.5 text-[12.5px] text-info"
        onClick={go}
        title={file ?? "在任务库中打开"}
      >
        {file ? <FileText className="h-3.5 w-3.5 shrink-0" /> : <ClipboardList className="h-3.5 w-3.5 shrink-0" />}
        <span className="truncate">{run.taskTitle || run.task || file}</span>
      </button>
      {file && <div className="mt-0.5 truncate font-mono text-[11px] text-faint">{file}</div>}
      {run.note && <div className="mt-1.5">{run.note}</div>}
      <button onClick={() => setOpen(!open)} className="mt-1 flex items-center gap-1 text-[12px] text-muted hover:text-fg">
        <ChevronRight className={clsx("h-3 w-3 transition-transform", open && "rotate-90")} />
        任务内容
      </button>
      {open && (
        <div className="mt-1 max-h-80 overflow-auto rounded-lg bg-bg px-3 py-2 text-[13px]">
          <Markdown text={body} />
        </div>
      )}
    </div>
  );
};

const assistantParts = {
  Text: MarkdownText,
  tools: { Fallback: ItemToolUI },
  data: {
    by_name: {
      reasoning: ReasoningPart,
      "run-header": RunHeaderPart,
      step: StepPart,
      activity: ActivityPart,
      result: ResultPart,
      process: ProcessPart,
      outcome: OutcomePart,
      manager: ManagerPart,
      "manager-busy": ManagerBusyPart,
      action: ActionPart,
      "task-done": TaskDonePart,
    },
  },
};

// ---------------------------------------------------------------- messages

const UserMessage: FC = () => {
  const custom = useAuiState((s) => s.message.metadata.custom) as unknown as MsgCustom | undefined;
  return (
    <MessagePrimitive.Root className="flex justify-end py-3" id={custom ? `run-${custom.runId}` : undefined}>
      <div className="max-w-[85%] whitespace-pre-wrap break-words rounded-2xl bg-sunken px-4 py-2.5 text-[14px]">
        <MessagePrimitive.Parts components={{ data: { by_name: { "workflow-request": WorkflowRequestPart, "task-request": TaskRequestPart } } }} />
      </div>
    </MessagePrimitive.Root>
  );
};

const AssistantMessage: FC = () => (
  <MessagePrimitive.Root className="py-2 text-[14px] leading-relaxed">
    <MessagePrimitive.Parts components={assistantParts} />
  </MessagePrimitive.Root>
);

// ---------------------------------------------------------------- composer

// On touch screens Enter inserts a newline; the send button sends.
const coarse = typeof window !== "undefined" && window.matchMedia("(pointer: coarse)").matches;

function Composer({ taskRunning, onTask }: { taskRunning: boolean; onTask: () => void }) {
  return (
    <ComposerPrimitive.Root className="rounded-2xl border border-line bg-bg shadow-sm focus-within:border-faint">
      <ComposerPrimitive.Input
        rows={1}
        autoFocus={!coarse}
        submitMode={coarse ? "none" : "enter"}
        placeholder={
          taskRunning ? "任务在后台执行，可以问管家进度，或让它改做法" : coarse ? "给 Codex 发送消息" : "给 Codex 发送消息，Enter 发送，Shift+Enter 换行"
        }
        className="max-h-60 min-h-[52px] w-full resize-none bg-transparent px-4 pt-3.5 text-[14px] outline-none placeholder:text-faint"
      />
      <div className="flex items-center gap-2 px-2.5 pb-2.5">
        <button type="button" className="btn-ghost" onClick={onTask} title="从任务库选一个任务，在这个会话里运行" disabled={taskRunning}>
          <ClipboardList className="h-4 w-4" /> 任务
        </button>
        {taskRunning && (
          <span className="flex min-w-0 items-center gap-1 truncate text-[12px] text-info">
            <Bot className="h-3.5 w-3.5 shrink-0" /> 发给管家
          </span>
        )}
        <div className="ml-auto flex items-center gap-1.5">
          <AuiIf condition={(s) => s.thread.isRunning}>
            <ComposerPrimitive.Cancel
              className="flex h-8 w-8 items-center justify-center rounded-full bg-accent text-bg hover:opacity-90"
              title="停止管家的回答"
            >
              <Square className="h-3.5 w-3.5 fill-current" />
            </ComposerPrimitive.Cancel>
          </AuiIf>
          <AuiIf condition={(s) => !s.thread.isRunning}>
            <ComposerPrimitive.Send
              className="flex h-8 w-8 items-center justify-center rounded-full bg-accent text-bg hover:opacity-90 disabled:opacity-30"
              title="发送"
            >
              <ArrowUp className="h-4 w-4" />
            </ComposerPrimitive.Send>
          </AuiIf>
        </div>
      </div>
    </ComposerPrimitive.Root>
  );
}

export function Thread({ taskRunning, onTask }: { taskRunning: boolean; onTask: () => void }) {
  return (
    <ThreadPrimitive.Root className="flex h-full min-h-0 flex-col">
      <ThreadPrimitive.Viewport className="relative flex min-h-0 flex-1 flex-col overflow-y-auto">
        <div className="mx-auto w-full max-w-3xl flex-1 px-3 pb-6 pt-3 md:px-4 md:pt-4">
          <ThreadPrimitive.Messages components={{ UserMessage, AssistantMessage }} />
        </div>
        <ThreadPrimitive.ViewportFooter className="sticky bottom-0 mx-auto w-full max-w-3xl bg-gradient-to-t from-bg via-bg to-transparent px-2 pb-2 pt-2 pb-safe md:px-4 md:pb-4">
          <ThreadPrimitive.ScrollToBottom className="absolute -top-9 left-1/2 flex h-8 w-8 -translate-x-1/2 items-center justify-center rounded-full border border-line bg-bg shadow-sm hover:bg-sunken disabled:invisible">
            <ArrowDown className="h-4 w-4" />
          </ThreadPrimitive.ScrollToBottom>
          <Composer taskRunning={taskRunning} onTask={onTask} />
        </ThreadPrimitive.ViewportFooter>
      </ThreadPrimitive.Viewport>
    </ThreadPrimitive.Root>
  );
}
