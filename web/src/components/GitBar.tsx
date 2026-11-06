import { useEffect, useState } from "react";
import clsx from "clsx";
import { ArrowDown, ArrowUp, ChevronDown, GitBranch, Loader2, RefreshCw, UploadCloud } from "lucide-react";
import { api } from "../api";
import { guard, navigate, useStore } from "../store";
import type { GitStatus } from "../types";
import { isActive } from "../types";
import { Sheet } from "./DirPicker";

const statusLabel: Record<string, string> = { M: "修改", A: "新增", D: "删除", R: "重命名", C: "复制", "?": "未跟踪", U: "冲突", T: "类型变更" };
const statusTone: Record<string, string> = { M: "text-warn", A: "text-ok", "?": "text-ok", D: "text-bad", U: "text-bad", R: "text-info" };

/** Git status of a session's directory, with a one-tap "commit & push by Codex". */
export function GitBar({ sessionId }: { sessionId: string }) {
  const session = useStore((s) => s.sessions[sessionId]);
  const lastRun = useStore((s) => {
    const ids = s.sessions[sessionId]?.runIds ?? [];
    return ids.length ? s.runs[ids[ids.length - 1]] : undefined;
  });
  const busy = isActive(session?.status);
  const [g, setG] = useState<GitStatus | null>(null);
  const [loading, setLoading] = useState(false);
  const [open, setOpen] = useState(false);
  const [note, setNote] = useState("");
  const [starting, setStarting] = useState(false);

  const load = () => {
    setLoading(true);
    api
      .git(sessionId)
      .then(setG)
      .catch(() => setG(null))
      .finally(() => setLoading(false));
  };
  // Refresh when the session's directory changes and whenever a task finishes.
  useEffect(load, [sessionId, session?.cwd, session?.nodeId, lastRun?.id, lastRun?.status === "running"]);

  if (!g?.repo) return null;
  const changes = g.files.length;
  const dirty = changes > 0 || g.ahead > 0;

  const sync = async () => {
    setStarting(true);
    const res = await guard(api.gitSync(sessionId, note));
    setStarting(false);
    if (res) {
      setOpen(false);
      setNote("");
    }
  };

  return (
    <>
      <div className="flex items-center gap-2 border-b border-line bg-panel px-3 py-1.5 text-[12.5px] md:px-4">
        <button className="flex min-w-0 items-center gap-1.5 text-muted hover:text-fg" onClick={() => setOpen(true)}>
          <GitBranch className="h-3.5 w-3.5 shrink-0" />
          <span className="truncate font-mono text-fg">{g.branch || "(detached)"}</span>
          {changes > 0 && <span className="shrink-0 text-warn">{changes} 处改动</span>}
          {g.ahead > 0 && (
            <span className="flex shrink-0 items-center text-info">
              <ArrowUp className="h-3 w-3" />
              {g.ahead}
            </span>
          )}
          {g.behind > 0 && (
            <span className="flex shrink-0 items-center text-muted">
              <ArrowDown className="h-3 w-3" />
              {g.behind}
            </span>
          )}
          {!dirty && <span className="shrink-0 text-faint">已同步</span>}
          <ChevronDown className="h-3 w-3 shrink-0" />
        </button>
        <div className="ml-auto flex items-center gap-1">
          <button className="btn-ghost !p-1" onClick={load} title="刷新">
            <RefreshCw className={clsx("h-3.5 w-3.5", loading && "animate-spin")} />
          </button>
          <button
            className="btn !py-0.5 !text-[12px]"
            disabled={busy || starting || !dirty}
            onClick={() => setOpen(true)}
            title={busy ? "任务执行中" : dirty ? "让 Codex 提交并推送" : "没有需要同步的改动"}
          >
            <UploadCloud className="h-3.5 w-3.5" /> 同步到 Git
          </button>
        </div>
      </div>
      {open && (
        <Sheet
          title={
            <span className="flex items-center gap-2">
              <GitBranch className="h-4 w-4" /> {g.branch} {g.upstream && <span className="text-[12px] font-normal text-faint">→ {g.upstream}</span>}
            </span>
          }
          onClose={() => setOpen(false)}
          footer={
            <>
              <button className="btn" onClick={() => setOpen(false)}>
                关闭
              </button>
              <button className="btn-primary" onClick={sync} disabled={busy || starting || !dirty}>
                {starting ? <Loader2 className="h-4 w-4 animate-spin" /> : <UploadCloud className="h-4 w-4" />} 让 Codex 提交并推送
              </button>
            </>
          }
        >
          <div className="space-y-3 p-4 text-[13px]">
            <div className="space-y-0.5 text-[12px] text-muted">
              {g.remote && (
                <div className="truncate">
                  远端 <code className="font-mono text-fg">{g.remote}</code>
                </div>
              )}
              {g.last && <div className="truncate">最近提交 {g.last}</div>}
              <div className="truncate">
                目录 <code className="font-mono">{g.root}</code>
              </div>
              {!g.upstream && <div className="text-warn">当前分支还没有上游，推送时会设置为 origin/{g.branch}</div>}
            </div>
            {changes > 0 ? (
              <div className="max-h-[40dvh] overflow-y-auto rounded-lg border border-line">
                {g.files.map((f) => (
                  <button
                    key={f.path}
                    className="flex w-full items-center gap-2 border-b border-line/50 px-3 py-2 text-left last:border-0 hover:bg-sunken"
                    onClick={() =>
                      session && navigate({ page: "files", node: session.nodeId, path: g.root, file: `${g.root}/${f.path}` })
                    }
                  >
                    <span className={clsx("w-12 shrink-0 text-[11.5px]", statusTone[f.status] ?? "text-muted")}>
                      {statusLabel[f.status] ?? f.status}
                    </span>
                    <span className="min-w-0 truncate font-mono text-[12.5px]">{f.path}</span>
                  </button>
                ))}
              </div>
            ) : (
              <div className="text-muted">{g.ahead > 0 ? `没有未提交的改动，有 ${g.ahead} 个提交未推送。` : "工作区干净，没有需要同步的内容。"}</div>
            )}
            <label className="block text-[12px] text-muted">
              给 Codex 的补充要求（可选）
              <input
                className="input mt-1"
                value={note}
                onChange={(e) => setNote(e.target.value)}
                placeholder="例如：只提交 src 目录；提交信息用英文"
              />
            </label>
            <p className="text-[11.5px] text-faint">
              Codex 会检查改动、跳过密钥和构建产物、写提交信息、先 pull --rebase 再 push，不会强制推送；遇到冲突会停下来告诉你。需要先在“设置”里配置 Git 密钥或令牌。
            </p>
          </div>
        </Sheet>
      )}
    </>
  );
}
