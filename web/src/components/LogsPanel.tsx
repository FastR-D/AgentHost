import { useEffect, useMemo, useRef, useState } from "react";
import clsx from "clsx";
import { Download, X } from "lucide-react";
import { useStore, closeLogs } from "../store";
import type { LogEntry } from "../types";

const sources = [
  { key: "host", label: "调度" },
  { key: "exec", label: "命令" },
  { key: "rpc", label: "Codex RPC" },
  { key: "codex", label: "Codex stderr" },
] as const;

const sourceTone: Record<string, string> = {
  host: "text-info",
  exec: "text-ok",
  rpc: "text-faint",
  codex: "text-warn",
};

const EMPTY: LogEntry[] = [];

/** Slide-over with the complete execution log of a run. */
export function LogsPanel() {
  const panel = useStore((s) => s.logsPanel);
  const run = useStore((s) => (panel ? s.runs[panel.runId] : undefined));
  const logs = useStore((s) => (panel ? s.logs[panel.runId] ?? EMPTY : EMPTY));
  const [enabled, setEnabled] = useState<Record<string, boolean>>({ host: true, exec: true, rpc: true, codex: true });
  const [step, setStep] = useState<string>(panel?.stepId ?? "");
  const [query, setQuery] = useState("");
  const [follow, setFollow] = useState(true);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => setStep(panel?.stepId ?? ""), [panel?.runId, panel?.stepId]);

  const shown = useMemo(() => {
    const q = query.toLowerCase();
    return logs.filter(
      (l) =>
        (enabled[l.source] ?? true) &&
        (!step || l.stepId === step || !l.stepId) &&
        (!q || l.text.toLowerCase().includes(q)),
    );
  }, [logs, enabled, step, query]);

  useEffect(() => {
    if (follow && ref.current) ref.current.scrollTop = ref.current.scrollHeight;
  }, [shown.length, follow]);

  if (!panel) return null;

  const download = () => {
    const text = logs.map((l) => `${l.time} [${l.source}]${l.stepId ? ` [${l.stepId}]` : ""} ${l.text}`).join("\n");
    const a = document.createElement("a");
    a.href = URL.createObjectURL(new Blob([text], { type: "text/plain" }));
    a.download = `${panel.runId}.log`;
    a.click();
    URL.revokeObjectURL(a.href);
  };

  return (
    <div className="fixed inset-0 z-30 flex justify-end bg-black/20" onClick={closeLogs}>
      <div className="flex h-full w-full max-w-3xl flex-col border-l border-line bg-bg pt-safe shadow-2xl" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center gap-2 border-b border-line px-4 py-3">
          <div className="min-w-0">
            <div className="font-medium">运行日志</div>
            <div className="truncate text-[12px] text-faint">
              {run?.title} · <span className="font-mono">{panel.runId}</span>
            </div>
          </div>
          <button className="btn-ghost ml-auto" onClick={download} title="下载日志">
            <Download className="h-4 w-4" />
          </button>
          <button className="btn-ghost" onClick={closeLogs}>
            <X className="h-4 w-4" />
          </button>
        </div>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-line px-3 py-2 text-[12px] md:px-4">
          {sources.map((s) => (
            <label key={s.key} className="flex items-center gap-1">
              <input
                type="checkbox"
                checked={enabled[s.key]}
                onChange={(e) => setEnabled({ ...enabled, [s.key]: e.target.checked })}
              />
              <span className={sourceTone[s.key]}>{s.label}</span>
            </label>
          ))}
          {run && run.steps.length > 1 && (
            <select className="input !w-auto !py-0.5" value={step} onChange={(e) => setStep(e.target.value)}>
              <option value="">全部步骤</option>
              {run.steps.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.title}
                </option>
              ))}
            </select>
          )}
          <input className="input !w-full !py-1 sm:!w-44" placeholder="搜索…" value={query} onChange={(e) => setQuery(e.target.value)} />
          <label className="ml-auto flex items-center gap-1 text-muted">
            <input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} /> 自动滚动
          </label>
          <span className="text-faint">{shown.length} 行</span>
        </div>
        <div ref={ref} className="min-h-0 flex-1 overflow-auto bg-panel px-2 py-2 font-mono text-[11.5px] leading-[1.6]">
          {shown.length === 0 && <div className="p-4 text-faint">暂无日志</div>}
          {shown.map((l) => (
            <div key={l.seq} className="flex gap-2 rounded px-2 hover:bg-sunken">
              <span className="shrink-0 text-faint">{new Date(l.time).toLocaleTimeString("zh-CN", { hour12: false })}</span>
              <span className={clsx("w-11 shrink-0", sourceTone[l.source] ?? "text-muted")}>{l.source}</span>
              {l.stepId && run && run.steps.length > 1 && <span className="shrink-0 text-faint">[{l.stepId}]</span>}
              <span className={clsx("min-w-0 whitespace-pre-wrap break-all", l.level === "error" && "text-bad", l.level === "warn" && "text-warn")}>
                {l.text}
              </span>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
