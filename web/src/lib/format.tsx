import { useEffect, useState } from "react";
import clsx from "clsx";
import { Check, CircleDashed, CircleSlash, Loader2, Pause, Square, X, Clock } from "lucide-react";
import type { Status } from "../types";

export const statusLabel: Record<string, string> = {
  queued: "排队中",
  pending: "等待中",
  running: "执行中",
  paused: "已暂停",
  completed: "已完成",
  failed: "失败",
  stopped: "已停止",
  skipped: "已跳过",
  declined: "已拒绝",
  "": "空闲",
};

export const statusTone: Record<string, string> = {
  queued: "text-muted",
  pending: "text-faint",
  running: "text-info",
  paused: "text-warn",
  completed: "text-ok",
  failed: "text-bad",
  stopped: "text-muted",
  skipped: "text-faint",
  declined: "text-warn",
};

export function StatusIcon({ status, className }: { status?: Status | string; className?: string }) {
  const cls = clsx("shrink-0", statusTone[status ?? ""] ?? "text-faint", className ?? "h-4 w-4");
  switch (status) {
    case "running":
      return <Loader2 className={clsx(cls, "animate-spin")} />;
    case "completed":
      return <Check className={cls} />;
    case "failed":
      return <X className={cls} />;
    case "paused":
      return <Pause className={cls} />;
    case "stopped":
      return <Square className={cls} />;
    case "skipped":
    case "declined":
      return <CircleSlash className={cls} />;
    case "queued":
      return <Clock className={cls} />;
    default:
      return <CircleDashed className={cls} />;
  }
}

export function StatusBadge({ status }: { status?: Status | string }) {
  const tone: Record<string, string> = {
    running: "bg-info/10 text-info border-info/30",
    completed: "bg-ok/10 text-ok border-ok/30",
    failed: "bg-bad/10 text-bad border-bad/30",
    paused: "bg-warn/10 text-warn border-warn/30",
    queued: "bg-sunken text-muted border-line",
  };
  return (
    <span
      className={clsx(
        "inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px] font-medium",
        tone[status ?? ""] ?? "border-line bg-sunken text-muted",
      )}
    >
      <StatusIcon status={status} className="h-3 w-3" />
      {statusLabel[status ?? ""] ?? status}
    </span>
  );
}

export function fmtDuration(ms?: number | null): string {
  if (ms == null || ms < 0 || !isFinite(ms)) return "";
  if (ms < 1000) return `${Math.round(ms)}ms`;
  const s = ms / 1000;
  if (s < 60) return `${s < 10 ? s.toFixed(1) : Math.round(s)}s`;
  const m = Math.floor(s / 60);
  const rs = Math.round(s % 60);
  if (m < 60) return `${m}m${rs ? ` ${rs}s` : ""}`;
  const h = Math.floor(m / 60);
  return `${h}h ${m % 60}m`;
}

export function between(a?: string, b?: string): number | undefined {
  if (!a) return undefined;
  const end = b ? Date.parse(b) : Date.now();
  return end - Date.parse(a);
}

export function fmtTime(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  const now = new Date();
  const sameDay = d.toDateString() === now.toDateString();
  const t = d.toLocaleTimeString("zh-CN", { hour12: false });
  return sameDay ? t : `${d.toLocaleDateString("zh-CN", { month: "2-digit", day: "2-digit" })} ${t}`;
}

export function fmtRelative(iso?: string): string {
  if (!iso) return "";
  const diff = Date.now() - Date.parse(iso);
  if (diff < 60_000) return "刚刚";
  if (diff < 3600_000) return `${Math.floor(diff / 60_000)} 分钟前`;
  if (diff < 86400_000) return `${Math.floor(diff / 3600_000)} 小时前`;
  return `${Math.floor(diff / 86400_000)} 天前`;
}

/** Re-renders every second while `active` so elapsed times tick. */
export function useTick(active: boolean, ms = 1000) {
  const [, setN] = useState(0);
  useEffect(() => {
    if (!active) return;
    const t = setInterval(() => setN((n) => n + 1), ms);
    return () => clearInterval(t);
  }, [active, ms]);
}

export function Elapsed({ start, end, running }: { start?: string; end?: string; running?: boolean }) {
  useTick(!!running && !end);
  const ms = between(start, end);
  if (ms == null) return null;
  return <span className="tabular-nums">{fmtDuration(ms)}</span>;
}

export function shortId(id?: string, n = 8) {
  if (!id) return "";
  return id.length > n ? id.slice(-n) : id;
}

export function fmtTokens(n?: number) {
  if (n == null) return "";
  if (n < 1000) return String(n);
  return `${(n / 1000).toFixed(n < 10000 ? 1 : 0)}k`;
}
