import { useEffect, useMemo, useState } from "react";
import { useShallow } from "zustand/react/shallow";
import { AssistantRuntimeProvider, useExternalStoreRuntime, type AppendMessage, type ThreadMessageLike } from "@assistant-ui/react";
import { FolderOpen, Loader2, Pencil, ScrollText, Trash2 } from "lucide-react";
import { api } from "../api";
import { useStore, loadSession, guard, isTaskRun, navigate, openLogs, sessionRuns, setCurrentNode, toast } from "../store";
import { isActive } from "../types";
import { shortId } from "../lib/format";
import { buildMessages } from "./messages";
import { Thread } from "./Thread";
import { WhereChip } from "./DirPicker";
import { GitBar } from "./GitBar";
import { TaskSheet, runTaskInSession } from "./TaskSheet";

function textOf(m: AppendMessage): string {
  return m.content
    .map((p) => (p.type === "text" ? p.text : ""))
    .join("\n")
    .trim();
}

export function ChatView({ sessionId }: { sessionId: string }) {
  const session = useStore((s) => s.sessions[sessionId]);
  const loaded = useStore((s) => !!s.loadedSessions[sessionId]);
  const runs = useStore(useShallow((s) => sessionRuns(s, sessionId)));
  const [error, setError] = useState<string | null>(null);
  const [taskOpen, setTaskOpen] = useState(false);

  useEffect(() => {
    setError(null);
    loadSession(sessionId).catch((e) => setError(e.message));
  }, [sessionId]);
  // A session lives on one server: opening it makes that server the current one.
  const sessionNode = session?.nodeId;
  useEffect(() => {
    if (sessionNode) setCurrentNode(sessionNode);
  }, [sessionNode]);

  // Jump to a specific run when the URL asks for it (from the task board).
  useEffect(() => {
    const target = sessionStorage.getItem("agenthost.scrollTo");
    if (target && loaded) {
      sessionStorage.removeItem("agenthost.scrollTo");
      setTimeout(() => document.getElementById(`run-${target}`)?.scrollIntoView({ block: "start" }), 300);
    }
  }, [loaded]);

  const messages = useMemo(() => buildMessages(runs), [runs]);
  // The task: the latest run that is not a manager reply. While it runs,
  // messages go to the manager, so only the manager's reply blocks the input.
  const tasks = runs.filter(isTaskRun);
  const lastTask = tasks[tasks.length - 1];
  const taskRunning = !!lastTask && isActive(lastTask.status);
  const activeManager = runs.find((r) => r.kind === "manager" && isActive(r.status));

  const runtime = useExternalStoreRuntime<ThreadMessageLike>({
    messages,
    isRunning: !!activeManager,
    convertMessage: (m) => m,
    onNew: async (m) => {
      const text = textOf(m);
      if (!text) return;
      const res = await guard(api.chat(sessionId, text));
      if (!res) throw new Error("send failed");
    },
    onCancel: async () => {
      if (activeManager) await guard(api.control(activeManager.id, "stop"));
    },
  });

  if (error) {
    return (
      <div className="flex h-full items-center justify-center text-muted">
        无法加载会话：{error}
        <button className="btn ml-3" onClick={() => navigate({ page: "home" })}>
          返回
        </button>
      </div>
    );
  }
  if (!session || !loaded) {
    return (
      <div className="flex h-full items-center justify-center text-faint">
        <Loader2 className="mr-2 h-4 w-4 animate-spin" /> 加载中…
      </div>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <SessionHeader sessionId={sessionId} lastRunId={lastTask?.id} />
      <div className="min-h-0 flex-1">
        <AssistantRuntimeProvider runtime={runtime}>
          <Thread taskRunning={taskRunning} onTask={() => setTaskOpen(true)} />
        </AssistantRuntimeProvider>
      </div>
      {taskOpen && <TaskSheet onClose={() => setTaskOpen(false)} onRun={(task, note, parallel) => runTaskInSession(sessionId, task, note, parallel)} />}
    </div>
  );
}

function SessionHeader({ sessionId, lastRunId }: { sessionId: string; lastRunId?: string }) {
  const session = useStore((s) => s.sessions[sessionId]);
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState(session?.title ?? "");
  if (!session) return null;

  const saveTitle = async () => {
    setEditing(false);
    if (title.trim() && title !== session.title) await guard(api.updateSession(sessionId, { title }));
  };
  const remove = async () => {
    if (!confirm(`删除会话「${session.title}」及其所有任务记录？正在运行的任务会被停止。`)) return;
    const ok = await guard(api.deleteSession(sessionId));
    if (ok) navigate({ page: "home" });
  };

  return (
    <div className="border-b border-line">
      <div className="flex items-center gap-2 px-3 py-2 md:px-4 md:py-2.5">
        {editing ? (
          <input
            autoFocus
            className="input !py-1 max-w-sm"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            onBlur={saveTitle}
            onKeyDown={(e) => {
              if (e.key === "Enter") void saveTitle();
              if (e.key === "Escape") setEditing(false);
            }}
          />
        ) : (
          <button
            className="group hidden min-w-0 items-center gap-1.5 font-medium md:flex"
            onClick={() => {
              setTitle(session.title);
              setEditing(true);
            }}
            title="重命名"
          >
            <span className="truncate">{session.title}</span>
            <Pencil className="h-3 w-3 shrink-0 text-faint opacity-0 group-hover:opacity-100" />
          </button>
        )}
        <div className="min-w-0 flex-1 md:flex-initial">
          <WhereChip
            node={session.nodeId}
            path={session.cwd}
            disabled={isActive(session.status)}
            onChange={async (dir, node) => {
              const res = await guard(api.updateSession(sessionId, { cwd: dir, nodeId: node }));
              if (res) toast("已切换目录，下一条消息将在新目录开启新的 Codex 会话", "info");
            }}
          />
        </div>
        {session.threadId && (
          <span className="hidden lg:inline-flex">
            <span className="chip font-mono" title={`Codex 线程 ${session.threadId}`}>
              thread {shortId(session.threadId)}
            </span>
          </span>
        )}
        <div className="ml-auto flex shrink-0 items-center">
          <button
            className="btn-ghost !p-2 md:!px-2 md:!py-1"
            onClick={() => navigate({ page: "files", node: session.nodeId, path: session.cwd })}
            title="查看这个会话目录里的文件"
          >
            <FolderOpen className="h-4 w-4" /> <span className="hidden md:inline">文件</span>
          </button>
          {lastRunId && (
            <button className="btn-ghost !p-2 md:!px-2 md:!py-1" onClick={() => openLogs(lastRunId)} title="最近一次任务的运行日志">
              <ScrollText className="h-4 w-4" /> <span className="hidden md:inline">日志</span>
            </button>
          )}
          <button className="btn-ghost !p-2 hover:!text-bad md:!px-2 md:!py-1" onClick={remove} title="删除会话">
            <Trash2 className="h-4 w-4" />
          </button>
        </div>
      </div>
      <GitBar sessionId={sessionId} />
    </div>
  );
}
