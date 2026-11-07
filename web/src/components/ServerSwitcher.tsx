import { useMemo, useState } from "react";
import clsx from "clsx";
import { Check, ChevronDown, Plus, Server } from "lucide-react";
import { navigate, nodeName, parseHash, setCurrentNode, useCurrentNode, useStore } from "../store";
import { isActive } from "../types";
import { Sheet } from "./DirPicker";

/** Switch to another server. Pages tied to the old one go back to their list. */
export function switchServer(id: string) {
  setCurrentNode(id);
  const r = parseHash(location.hash);
  if (r.page === "session") navigate({ page: "home" });
  else if (r.page === "files") navigate({ page: "files", node: id });
  else if (r.page === "tasks" && r.name) navigate({ page: "tasks" });
}

export function ServerSheet({ onClose }: { onClose: () => void }) {
  const nodes = useStore((s) => s.nodes);
  const runs = useStore((s) => s.runs);
  const current = useCurrentNode();
  const active = useMemo(() => {
    const c: Record<string, number> = {};
    for (const r of Object.values(runs)) if (r.kind !== "manager" && isActive(r.status)) c[r.nodeId] = (c[r.nodeId] ?? 0) + 1;
    return c;
  }, [runs]);
  return (
    <Sheet
      title={
        <span className="flex items-center gap-2">
          <Server className="h-4 w-4 text-info" /> 选择服务器
        </span>
      }
      onClose={onClose}
      footer={
        <button
          className="btn mr-auto"
          onClick={() => {
            onClose();
            navigate({ page: "servers" });
          }}
        >
          <Plus className="h-4 w-4" /> 接入新服务器
        </button>
      }
    >
      <div className="space-y-1 p-3">
        <p className="px-1 pb-2 text-[12px] leading-relaxed text-faint">
          选好一台服务器后，新任务、任务库、文件、会话和运行记录都只针对这台。
        </p>
        {nodes.map((n) => (
          <button
            key={n.id}
            onClick={() => {
              switchServer(n.id);
              onClose();
            }}
            className={clsx(
              "flex w-full items-start gap-3 rounded-xl border px-3 py-3 text-left transition",
              n.id === current ? "border-info bg-info/5" : "border-transparent hover:bg-sunken",
            )}
          >
            <span className={clsx("mt-1.5 h-2.5 w-2.5 shrink-0 rounded-full", n.online ? "bg-ok" : "bg-faint")} />
            <span className="min-w-0 flex-1">
              <span className="flex items-center gap-2">
                <span className="truncate text-[15px] font-medium">{n.name}</span>
                {n.local && <span className="chip shrink-0">控制台本机</span>}
              </span>
              <span className="block truncate font-mono text-[11.5px] text-faint">{n.workspace || "—"}</span>
              <span className="block text-[12px] text-muted">
                {!n.online ? "离线" : active[n.id] ? <span className="text-info">{active[n.id]} 个任务运行中</span> : "在线 · 空闲"}
              </span>
            </span>
            {n.id === current && <Check className="mt-1 h-4 w-4 shrink-0 text-info" />}
          </button>
        ))}
      </div>
    </Sheet>
  );
}

/** The current server, shown at the top of the sidebar; tap to switch. */
export function ServerButton({ className }: { className?: string }) {
  const [open, setOpen] = useState(false);
  const current = useCurrentNode();
  const info = useStore((s) => s.nodes.find((n) => n.id === current));
  const count = useStore((s) => s.nodes.length);
  return (
    <>
      <button
        className={clsx("flex w-full items-center gap-2 rounded-lg border border-line bg-bg px-2.5 py-2 text-left hover:border-faint", className)}
        onClick={() => setOpen(true)}
        title="切换服务器"
      >
        <Server className="h-4 w-4 shrink-0 text-muted" />
        <span className="min-w-0 flex-1">
          <span className="block text-[11px] leading-tight text-faint">当前服务器{count > 1 ? ` · 共 ${count} 台` : ""}</span>
          <span className="flex items-center gap-1.5 text-[14px] font-medium leading-tight">
            <span className={clsx("h-2 w-2 shrink-0 rounded-full", info?.online ? "bg-ok" : "bg-faint")} />
            <span className="truncate">{nodeName(current)}</span>
            {info && !info.online && <span className="shrink-0 text-[12px] font-normal text-bad">离线</span>}
          </span>
        </span>
        <ChevronDown className="h-4 w-4 shrink-0 text-faint" />
      </button>
      {open && <ServerSheet onClose={() => setOpen(false)} />}
    </>
  );
}
