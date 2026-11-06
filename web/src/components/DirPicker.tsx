import { useEffect, useState } from "react";
import clsx from "clsx";
import { ArrowUp, Folder, FolderOpen, Server, X } from "lucide-react";
import { api } from "../api";
import { useStore, nodeName } from "../store";

export interface Location {
  node: string;
  path: string;
}

/** Bottom sheet on phones, dialog on desktop. */
export function Sheet({
  title,
  onClose,
  children,
  footer,
  wide,
}: {
  title: React.ReactNode;
  onClose: () => void;
  children: React.ReactNode;
  footer?: React.ReactNode;
  wide?: boolean;
}) {
  return (
    <div className="fixed inset-0 z-50 flex items-end justify-center bg-black/30 md:items-center md:p-4" onClick={onClose}>
      <div
        className={clsx(
          "flex max-h-[92dvh] w-full flex-col rounded-t-2xl border border-line bg-bg shadow-xl md:max-h-[85vh] md:rounded-2xl",
          wide ? "md:max-w-3xl" : "md:max-w-lg",
        )}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center border-b border-line px-4 py-3">
          <div className="min-w-0 flex-1 truncate font-medium">{title}</div>
          <button className="btn-ghost -mr-2 !p-2" onClick={onClose} aria-label="关闭">
            <X className="h-5 w-5" />
          </button>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto">{children}</div>
        {footer && <div className="flex justify-end gap-2 border-t border-line px-4 py-3 pb-safe">{footer}</div>}
      </div>
    </div>
  );
}

/** Pick a directory on a server (the current one unless allowNode). */
export function DirPicker({
  initial,
  node,
  allowNode = false,
  onPick,
  onClose,
}: {
  initial?: string;
  node?: string;
  allowNode?: boolean;
  onPick: (dir: string, node: string) => void;
  onClose: () => void;
}) {
  const nodes = useStore((s) => s.nodes);
  const defaultNode = useStore((s) => s.defaultNode);
  const [nd, setNd] = useState(node || defaultNode);
  const [path, setPath] = useState(initial ?? "");
  const [input, setInput] = useState(initial ?? "");
  const [dirs, setDirs] = useState<string[]>([]);
  const [parent, setParent] = useState("");
  const [err, setErr] = useState("");
  const current = nodes.find((n) => n.id === nd);

  const load = async (p: string | undefined, n = nd) => {
    try {
      const res = await api.dirs(p, n);
      setPath(res.path);
      setInput(res.path);
      setParent(res.parent);
      setDirs(res.dirs);
      setErr("");
    } catch (e: any) {
      setErr(e.message);
      setDirs([]);
    }
  };
  useEffect(() => {
    void load(initial);
  }, []);

  const switchNode = (id: string) => {
    setNd(id);
    void load(undefined, id); // start at that server's workspace
  };

  return (
    <Sheet
      title={allowNode ? "选择服务器和工作目录" : `选择目录 · ${nodeName(nd)}`}
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            取消
          </button>
          <button className="btn-primary" disabled={!current?.online} onClick={() => onPick(input.trim() || path, nd)}>
            使用此目录
          </button>
        </>
      }
    >
      {allowNode && nodes.length > 1 && (
        <div className="border-b border-line px-4 py-3">
          <div className="mb-2 text-[12px] text-faint">服务器</div>
          <div className="flex flex-wrap gap-1.5">
            {nodes.map((n) => (
              <button
                key={n.id}
                disabled={!n.online}
                onClick={() => switchNode(n.id)}
                className={clsx(
                  "flex items-center gap-1.5 rounded-lg border px-3 py-2 text-[13px] disabled:opacity-40",
                  nd === n.id ? "border-info bg-info/10 text-fg" : "border-line hover:bg-sunken",
                )}
              >
                <span className={clsx("h-2 w-2 rounded-full", n.online ? "bg-ok" : "bg-faint")} />
                {n.name}
              </button>
            ))}
          </div>
        </div>
      )}
      <div className="space-y-2 p-4">
        <div className="flex gap-2">
          <input
            className="input font-mono"
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && load(input)}
            placeholder="/path/to/project（不存在会自动创建）"
          />
          <button className="btn" onClick={() => load(parent)} disabled={!parent || parent === path} title="上一级">
            <ArrowUp className="h-4 w-4" />
          </button>
        </div>
        {err && <div className="text-[12px] text-bad">{err}</div>}
        <div className="max-h-[40dvh] min-h-[180px] overflow-auto rounded-lg border border-line md:h-64">
          {dirs.length === 0 && !err && <div className="p-3 text-[12px] text-faint">没有子目录</div>}
          {dirs.map((d) => (
            <button
              key={d}
              className="flex w-full items-center gap-2 px-3 py-2.5 text-left text-[14px] hover:bg-sunken md:py-1.5 md:text-[13px]"
              onClick={() => load(`${path.replace(/\/$/, "")}/${d}`)}
            >
              <Folder className="h-4 w-4 text-faint" /> {d}
            </button>
          ))}
        </div>
      </div>
    </Sheet>
  );
}

/** Chip showing the directory (on the current server); opens the picker. */
export function WhereChip({
  node,
  path,
  placeholder,
  onChange,
  disabled,
  allowNode = false,
}: {
  node?: string;
  path?: string;
  placeholder?: string;
  onChange: (path: string, node: string) => void;
  disabled?: boolean;
  allowNode?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const nodes = useStore((s) => s.nodes);
  const defaultNode = useStore((s) => s.defaultNode);
  const nd = node || defaultNode;
  const info = nodes.find((n) => n.id === nd);
  const multi = allowNode && nodes.length > 1;
  return (
    <>
      <button
        type="button"
        className="chip min-w-0 max-w-full cursor-pointer !py-1 hover:text-fg"
        onClick={() => setOpen(true)}
        disabled={disabled}
        title={allowNode ? "选择服务器和工作目录" : `选择目录 · ${nodeName(nd)}`}
      >
        {multi ? <Server className="h-3 w-3 shrink-0" /> : <FolderOpen className="h-3 w-3 shrink-0" />}
        {multi && (
          <span className={clsx("shrink-0", info && !info.online && "text-bad")}>
            {nodeName(nd)}
            {info && !info.online ? "（离线）" : ""}
          </span>
        )}
        {multi && <span className="shrink-0 text-faint">·</span>}
        <span className="min-w-0 truncate font-mono">{path || placeholder || info?.workspace || "默认工作区"}</span>
      </button>
      {open && (
        <DirPicker
          initial={path || undefined}
          node={nd}
          allowNode={allowNode}
          onClose={() => setOpen(false)}
          onPick={(p, n) => {
            setOpen(false);
            onChange(p, n);
          }}
        />
      )}
    </>
  );
}
