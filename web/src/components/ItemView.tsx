import { useEffect, useMemo, useRef, useState } from "react";
import clsx from "clsx";
import {
  AlertTriangle,
  Bot,
  Brain,
  ChevronRight,
  FileDiff,
  FilePlus2,
  FileMinus2,
  FileEdit,
  Globe,
  Info,
  ListChecks,
  Sparkles,
  Terminal,
  Wrench,
  Workflow as WorkflowIcon,
  Circle,
  CircleCheck,
  CircleDot,
  XCircle,
} from "lucide-react";
import type { Item } from "../types";
import { StatusIcon, fmtDuration, between, Elapsed, shortId } from "../lib/format";
import { Markdown } from "./Markdown";
import { ChildRunView } from "./StepCard";

/** "/bin/bash -lc 'npm test'" → "npm test" */
export function prettyCommand(cmd?: string): string {
  if (!cmd) return "";
  const m = cmd.match(/^(?:\/\S*\/)?(?:ba|z)?sh\s+-l?c\s+'([\s\S]*)'$/);
  if (m) return m[1].replace(/'\\''/g, "'");
  const m2 = cmd.match(/^(?:\/\S*\/)?(?:ba|z)?sh\s+-l?c\s+"([\s\S]*)"$/);
  if (m2) return m2[1];
  return cmd;
}

function Disclosure({
  open,
  onToggle,
  header,
  children,
  className,
}: {
  open: boolean;
  onToggle: () => void;
  header: React.ReactNode;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={clsx("overflow-hidden rounded-lg border border-line bg-bg", className)}>
      <button
        type="button"
        onClick={onToggle}
        className="flex w-full items-center gap-2 px-3 py-2 text-left text-[13px] hover:bg-panel"
      >
        <ChevronRight className={clsx("h-3.5 w-3.5 shrink-0 text-faint transition-transform", open && "rotate-90")} />
        {header}
      </button>
      {open && <div className="border-t border-line">{children}</div>}
    </div>
  );
}

function OutputBox({ text, live, tone }: { text: string; live?: boolean; tone?: "bad" }) {
  const ref = useRef<HTMLPreElement>(null);
  useEffect(() => {
    if (live && ref.current) ref.current.scrollTop = ref.current.scrollHeight;
  }, [text, live]);
  if (!text) return <div className="px-3 py-2 text-[12px] italic text-faint">（无输出）</div>;
  return (
    <pre
      ref={ref}
      className={clsx("term max-h-[50dvh] overflow-auto bg-sunken/60 px-3 py-2 md:max-h-[360px]", tone === "bad" ? "text-bad" : "text-fg")}
    >
      {text}
    </pre>
  );
}

export function CommandCard({ item, defaultOpen }: { item: Item; defaultOpen?: boolean }) {
  const running = item.status === "running";
  const failed = item.status === "failed" || (item.exitCode != null && item.exitCode !== 0);
  const [open, setOpen] = useState<boolean | null>(null);
  const isOpen = open ?? (defaultOpen || running || failed);
  const hasSplit = item.stdout !== undefined || item.stderr !== undefined;
  const [tab, setTab] = useState<"all" | "stdout" | "stderr">("all");
  const cmd = prettyCommand(item.command);
  const dur = item.durationMs || between(item.startedAt, item.endedAt);
  return (
    <Disclosure
      open={isOpen}
      onToggle={() => setOpen(!isOpen)}
      header={
        <>
          <StatusIcon status={failed && item.status !== "running" ? "failed" : item.status} className="h-3.5 w-3.5" />
          <Terminal className="h-3.5 w-3.5 shrink-0 text-faint" />
          <code className="min-w-0 flex-1 truncate font-mono text-[12.5px]">{cmd.split("\n")[0]}</code>
          {item.exitCode != null && (
            <span className={clsx("chip font-mono", item.exitCode === 0 ? "" : "border-bad/40 text-bad")}>
              exit {item.exitCode}
            </span>
          )}
          <span className="shrink-0 text-[11px] text-faint">
            {running ? <Elapsed start={item.startedAt} running /> : dur ? fmtDuration(dur) : ""}
          </span>
        </>
      }
    >
      <div className="space-y-1 px-3 py-2 text-[12px] text-muted">
        {cmd.includes("\n") && <pre className="term rounded bg-sunken/60 px-2 py-1.5 text-fg">{cmd}</pre>}
        <div className="flex flex-wrap gap-x-4 gap-y-1">
          <span>
            工作目录 <code className="font-mono text-fg">{item.cwd || "—"}</code>
          </span>
          {item.argv && item.argv.length > 0 && (
            <span className="min-w-0 truncate">
              进程参数{" "}
              <code className="font-mono text-fg">
                {item.argv
                  .map((a, i) =>
                    i === item.argv!.length - 1 && a === item.command && a.includes("\n")
                      ? "<上方脚本>"
                      : a.includes(" ") || a.includes("\n")
                        ? JSON.stringify(a)
                        : a,
                  )
                  .join(" ")}
              </code>
            </span>
          )}
          {!item.argv && item.command && item.command !== cmd && (
            <span className="min-w-0 truncate">
              原始命令 <code className="font-mono text-fg">{item.command}</code>
            </span>
          )}
          {item.threadId && <span>子 Agent 线程 {shortId(item.threadId)}</span>}
        </div>
      </div>
      {hasSplit ? (
        <>
          <div className="flex gap-1 border-y border-line bg-panel px-2 py-1 text-[12px]">
            {(["all", "stdout", "stderr"] as const).map((t) => (
              <button
                key={t}
                onClick={() => setTab(t)}
                className={clsx("rounded px-2 py-0.5", tab === t ? "bg-bg text-fg shadow-sm" : "text-muted hover:text-fg")}
              >
                {t === "all" ? "全部输出" : t}
                {t === "stderr" && item.stderr ? <span className="ml-1 text-bad">●</span> : null}
              </button>
            ))}
          </div>
          <OutputBox
            text={tab === "all" ? item.output ?? "" : tab === "stdout" ? item.stdout ?? "" : item.stderr ?? ""}
            live={running}
            tone={tab === "stderr" ? "bad" : undefined}
          />
        </>
      ) : (
        <>
          <div className="border-y border-line bg-panel px-3 py-1 text-[11px] text-faint">输出（Codex 合并了 stdout 与 stderr）</div>
          <OutputBox text={item.output ?? ""} live={running} />
        </>
      )}
    </Disclosure>
  );
}

function DiffView({ diff }: { diff: string }) {
  const lines = useMemo(() => diff.split("\n"), [diff]);
  return (
    <pre className="term max-h-[420px] overflow-auto bg-sunken/40 py-1">
      {lines.map((l, i) => (
        <div
          key={i}
          className={clsx(
            "px-3",
            l.startsWith("+") && !l.startsWith("+++") && "bg-ok/10 text-ok",
            l.startsWith("-") && !l.startsWith("---") && "bg-bad/10 text-bad",
            l.startsWith("@@") && "text-info",
          )}
        >
          {l || " "}
        </div>
      ))}
    </pre>
  );
}

export { DiffView };

function kindIcon(kind: string) {
  if (kind === "add") return <FilePlus2 className="h-3.5 w-3.5 text-ok" />;
  if (kind === "delete") return <FileMinus2 className="h-3.5 w-3.5 text-bad" />;
  return <FileEdit className="h-3.5 w-3.5 text-info" />;
}

export function FileChangeCard({ item }: { item: Item }) {
  const [open, setOpen] = useState(false);
  const [diffOpen, setDiffOpen] = useState<Record<number, boolean>>({});
  const changes = item.changes ?? [];
  return (
    <Disclosure
      open={open}
      onToggle={() => setOpen(!open)}
      header={
        <>
          <StatusIcon status={item.status} className="h-3.5 w-3.5" />
          <FileDiff className="h-3.5 w-3.5 shrink-0 text-faint" />
          <span className="min-w-0 flex-1 truncate">
            {item.status === "running" ? "正在修改" : "修改了"} {changes.length} 个文件
            <span className="ml-2 font-mono text-[12px] text-muted">
              {changes
                .slice(0, 3)
                .map((c) => c.path.split("/").pop())
                .join(", ")}
              {changes.length > 3 ? " …" : ""}
            </span>
          </span>
        </>
      }
    >
      <div className="divide-y divide-line">
        {changes.map((c, i) => (
          <div key={i}>
            <button
              onClick={() => setDiffOpen((d) => ({ ...d, [i]: !d[i] }))}
              className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-[12.5px] hover:bg-panel"
            >
              {kindIcon(c.kind)}
              <code className="min-w-0 flex-1 truncate font-mono">{c.path}</code>
              <span className="chip">{c.kind}</span>
            </button>
            {diffOpen[i] && (c.diff ? <DiffView diff={c.diff} /> : <div className="px-3 py-2 text-[12px] text-faint">无 diff</div>)}
          </div>
        ))}
      </div>
    </Disclosure>
  );
}

export function PlanCard({ item }: { item: Item }) {
  const plan = item.plan ?? [];
  return (
    <div className="rounded-lg border border-line bg-bg px-3 py-2">
      <div className="mb-1 flex items-center gap-2 text-[12px] font-medium text-muted">
        <ListChecks className="h-3.5 w-3.5" /> {item.title || "计划"}
        {plan.length > 0 && (
          <span className="text-faint">
            {plan.filter((p) => p.status === "completed").length}/{plan.length}
          </span>
        )}
      </div>
      {item.text && <div className="mb-1 text-[12.5px] text-muted">{item.text}</div>}
      <ul className="space-y-0.5">
        {plan.map((p, i) => (
          <li key={i} className="flex items-start gap-2 text-[13px]">
            {p.status === "completed" ? (
              <CircleCheck className="mt-0.5 h-3.5 w-3.5 shrink-0 text-ok" />
            ) : p.status === "failed" ? (
              <XCircle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-bad" />
            ) : p.status === "running" ? (
              <CircleDot className="mt-0.5 h-3.5 w-3.5 shrink-0 text-info" />
            ) : (
              <Circle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-faint" />
            )}
            <span className="min-w-0">
              <span className={clsx(p.status === "completed" && "text-muted", p.status === "skipped" && "text-faint line-through")}>
                {p.step}
              </span>
              {p.note && <span className="ml-2 text-[12px] text-faint">{p.note}</span>}
            </span>
          </li>
        ))}
      </ul>
      {!plan.length && item.text === undefined && <div className="text-[12px] text-faint">（空）</div>}
    </div>
  );
}

export function ToolCard({ item }: { item: Item }) {
  const [open, setOpen] = useState(false);
  const args = item.arguments === undefined ? "" : JSON.stringify(item.arguments, null, 2);
  const dur = item.durationMs || between(item.startedAt, item.endedAt);
  return (
    <Disclosure
      open={open}
      onToggle={() => setOpen(!open)}
      header={
        <>
          <StatusIcon status={item.status} className="h-3.5 w-3.5" />
          <Wrench className="h-3.5 w-3.5 shrink-0 text-faint" />
          <span className="min-w-0 flex-1 truncate">
            调用工具 <code className="font-mono text-[12.5px]">{[item.server, item.tool].filter(Boolean).join(" · ")}</code>
          </span>
          <span className="text-[11px] text-faint">{dur ? fmtDuration(dur) : ""}</span>
        </>
      }
    >
      <div className="space-y-2 px-3 py-2 text-[12px]">
        {args && (
          <div>
            <div className="mb-1 text-faint">参数</div>
            <pre className="term max-h-60 overflow-auto rounded bg-sunken/60 px-2 py-1.5">{args}</pre>
          </div>
        )}
        {item.result && (
          <div>
            <div className="mb-1 text-faint">结果</div>
            <pre className="term max-h-60 overflow-auto rounded bg-sunken/60 px-2 py-1.5">{item.result}</pre>
          </div>
        )}
        {item.text && <div className="text-bad">{item.text}</div>}
      </div>
    </Disclosure>
  );
}

export function AgentCard({ item }: { item: Item }) {
  const [open, setOpen] = useState(true);
  if (item.tool === "workflow" && item.text) {
    return (
      <Disclosure
        open={open}
        onToggle={() => setOpen(!open)}
        header={
          <>
            <WorkflowIcon className="h-3.5 w-3.5 shrink-0 text-info" />
            <span className="min-w-0 flex-1 truncate font-medium">{item.title}</span>
            <span className="chip font-mono">{shortId(item.text, 10)}</span>
          </>
        }
      >
        <div className="p-2">
          <ChildRunView runId={item.text} />
        </div>
      </Disclosure>
    );
  }
  let states = "";
  if (item.result) {
    try {
      states = JSON.stringify(JSON.parse(item.result), null, 2);
    } catch {
      states = item.result;
    }
  }
  return (
    <Disclosure
      open={open && !!(item.text || states)}
      onToggle={() => setOpen(!open)}
      header={
        <>
          <StatusIcon status={item.status} className="h-3.5 w-3.5" />
          <Bot className="h-3.5 w-3.5 shrink-0 text-info" />
          <span className="min-w-0 flex-1 truncate">{item.title || `Agent ${item.tool}`}</span>
          {item.server && <span className="chip">{item.server}</span>}
        </>
      }
    >
      <div className="space-y-2 px-3 py-2 text-[12.5px]">
        {item.text && <div className="whitespace-pre-wrap text-muted">{item.text}</div>}
        {states && states !== "{}" && <pre className="term rounded bg-sunken/60 px-2 py-1.5">{states}</pre>}
      </div>
    </Disclosure>
  );
}

export function ReasoningView({ text, running, startedAt, endedAt }: { text?: string; running?: boolean; startedAt?: string; endedAt?: string }) {
  const [open, setOpen] = useState(false);
  if (running) {
    return (
      <div className="py-0.5">
        <div className="flex items-center gap-2 text-[13px]">
          <Brain className="h-3.5 w-3.5 text-faint" />
          <span className="shimmer-text font-medium">思考中…</span>
        </div>
        {text && <div className="ml-5 mt-1 whitespace-pre-wrap text-[13px] italic text-muted">{text.slice(-600)}</div>}
      </div>
    );
  }
  if (!text) return null;
  const dur = between(startedAt, endedAt);
  const title = text.match(/^\*\*(.+?)\*\*/)?.[1];
  return (
    <div className="py-0.5">
      <button onClick={() => setOpen(!open)} className="flex items-center gap-2 text-[13px] text-muted hover:text-fg">
        <Brain className="h-3.5 w-3.5 text-faint" />
        <span>{title ? `思考：${title}` : "思考过程"}</span>
        {dur ? <span className="text-faint">{fmtDuration(dur)}</span> : null}
        <ChevronRight className={clsx("h-3 w-3 transition-transform", open && "rotate-90")} />
      </button>
      {open && <Markdown text={text} className="ml-5 mt-1 border-l-2 border-line pl-3 text-muted [&_*]:text-muted" />}
    </div>
  );
}

export function NoticeRow({ item }: { item: Item }) {
  const bad = item.kind === "error";
  return (
    <div
      className={clsx(
        "flex items-start gap-2 rounded-lg px-3 py-1.5 text-[12.5px]",
        bad ? "bg-bad/10 text-bad" : "bg-panel text-muted",
      )}
    >
      {bad ? <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" /> : <Info className="mt-0.5 h-3.5 w-3.5 shrink-0" />}
      <div className="min-w-0">
        {item.title && <div className="font-medium">{item.title}</div>}
        {item.text && <div className="whitespace-pre-wrap break-words opacity-90">{item.text}</div>}
      </div>
    </div>
  );
}

/** Which skills from the task's code this Codex process was given: one line, tap for details. */
export function SkillsRow({ item }: { item: Item }) {
  const lines = (item.text ?? "").split("\n").filter(Boolean);
  const warn = lines.some((l) => l.startsWith("⚠"));
  const [open, setOpen] = useState(warn);
  const names = Array.isArray(item.raw) ? (item.raw as { name?: string }[]).map((s) => s.name).filter(Boolean) : [];
  const title = (item.title ?? "").replace(/（来自 .*）$/, "");
  const from = /（来自 (.*)）$/.exec(item.title ?? "")?.[1];
  return (
    <div className="rounded-lg bg-info/5 text-[12.5px] text-muted">
      <button className="flex w-full items-center gap-2 px-3 py-1.5 text-left" onClick={() => setOpen(!open)}>
        <Sparkles className="h-3.5 w-3.5 shrink-0 text-info" />
        <span className="min-w-0 flex-1 truncate">
          <span className="text-fg">{title}</span>
          {names.length > 0 && <span className="text-faint"> · {names.join("、")}</span>}
        </span>
        <ChevronRight className={clsx("h-3.5 w-3.5 shrink-0 text-faint transition-transform", open && "rotate-90")} />
      </button>
      {open && (
        <div className="space-y-0.5 px-3 pb-2 pl-8">
          {from && <div className="break-all text-faint">来自 {from}</div>}
          {lines.map((l, i) => (
            <div key={i} className={clsx("break-words", l.startsWith("⚠") && "text-warn")}>
              {l}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function OtherCard({ item }: { item: Item }) {
  const [open, setOpen] = useState(false);
  return (
    <Disclosure
      open={open}
      onToggle={() => setOpen(!open)}
      header={
        <>
          <StatusIcon status={item.status} className="h-3.5 w-3.5" />
          <span className="min-w-0 flex-1 truncate text-muted">
            {item.title || "事件"} {item.text && <code className="font-mono text-[12px]">{item.text}</code>}
          </span>
        </>
      }
    >
      <pre className="term max-h-60 overflow-auto px-3 py-2">{JSON.stringify(item.raw ?? item, null, 2)}</pre>
    </Disclosure>
  );
}

/** Renders any timeline item. Messages are rendered as markdown. */
export function ItemView({ item }: { item: Item }) {
  switch (item.kind) {
    case "message":
      return item.text ? <Markdown text={item.text} /> : null;
    case "reasoning":
      return <ReasoningView text={item.text} running={item.status === "running"} startedAt={item.startedAt} endedAt={item.endedAt} />;
    case "command":
      return <CommandCard item={item} />;
    case "file_change":
      return <FileChangeCard item={item} />;
    case "tool":
      return <ToolCard item={item} />;
    case "web_search":
      return (
        <div className="flex items-center gap-2 text-[13px] text-muted">
          <StatusIcon status={item.status} className="h-3.5 w-3.5" />
          <Globe className="h-3.5 w-3.5 text-faint" /> 搜索网页 <span className="text-fg">{item.text}</span>
        </div>
      );
    case "plan":
      return <PlanCard item={item} />;
    case "agent":
      return <AgentCard item={item} />;
    case "error":
    case "notice":
      return <NoticeRow item={item} />;
    case "skills":
      return <SkillsRow item={item} />;
    default:
      return <OtherCard item={item} />;
  }
}
