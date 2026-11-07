import { useMemo } from "react";
import clsx from "clsx";
import { ClipboardList, FolderOpen, LayoutList, LogOut, MessageSquarePlus, Server, ServerCrash, Settings, Trash2 } from "lucide-react";
import { api } from "../api";
import { useStore, navigate, guard, useCurrentNode, type Route } from "../store";
import { isActive } from "../types";
import { StatusIcon, fmtRelative, useTick } from "../lib/format";
import { ServerButton } from "./ServerSwitcher";

export function Sidebar({ route, className }: { route: Route; className?: string }) {
  const sessions = useStore((s) => s.sessions);
  const runs = useStore((s) => s.runs);
  const connected = useStore((s) => s.connected);
  const info = useStore((s) => s.info);
  const nodes = useStore((s) => s.nodes);
  const current = useCurrentNode();
  useTick(true, 30_000);

  // Only the current server's sessions and runs.
  const list = useMemo(
    () =>
      Object.values(sessions)
        .filter((s) => s.nodeId === current)
        .sort((a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt)),
    [sessions, current],
  );
  const activeCount = useMemo(
    () => Object.values(runs).filter((r) => isActive(r.status) && r.kind !== "manager" && r.nodeId === current).length,
    [runs, current],
  );

  const remove = async (id: string, title: string) => {
    if (!confirm(`删除会话「${title}」及其所有任务记录？`)) return;
    await guard(api.deleteSession(id));
    if (route.page === "session" && route.id === id) navigate({ page: "home" });
  };

  const navItem = (active: boolean) =>
    clsx(
      "flex w-full items-center gap-2 rounded-lg px-2.5 py-2.5 text-[14px] transition md:py-1.5 md:text-[13px]",
      active ? "bg-sunken font-medium text-fg" : "text-muted hover:bg-sunken hover:text-fg",
    );

  return (
    <aside className={clsx("flex h-full w-[280px] shrink-0 flex-col border-r border-line bg-panel md:w-[264px]", className)}>
      <div className="flex items-center gap-2 px-4 pb-2 pt-4">
        <div className="flex h-7 w-7 items-center justify-center rounded-lg bg-accent text-[13px] font-bold text-bg">A</div>
        <div className="leading-tight">
          <div className="font-semibold">AgentHost</div>
          <div className="text-[11px] text-faint">Codex CLI 远程控制台</div>
        </div>
      </div>
      <div className="px-2 pt-1">
        <ServerButton />
      </div>
      {!nodes.some((n) => n.online) && (
        <button
          className="mx-2 mt-1 flex items-center gap-2 rounded-lg border border-dashed border-warn/50 bg-warn/5 px-3 py-2.5 text-left text-[13px] text-warn transition hover:bg-warn/10"
          onClick={() => navigate({ page: "servers" })}
        >
          <ServerCrash className="h-4 w-4 shrink-0" />
          <span>没有在线的服务器，点击添加</span>
        </button>
      )}
      <div className="space-y-0.5 px-2 py-2">
        <button className={navItem(route.page === "home")} onClick={() => navigate({ page: "home" })}>
          <MessageSquarePlus className="h-4 w-4" /> 新任务
        </button>
        <button className={navItem(route.page === "tasks")} onClick={() => navigate({ page: "tasks" })}>
          <ClipboardList className="h-4 w-4" /> 任务库
        </button>
        <button className={navItem(route.page === "runs")} onClick={() => navigate({ page: "runs" })}>
          <LayoutList className="h-4 w-4" /> 运行记录
          {activeCount > 0 && (
            <span className="ml-auto rounded-full bg-info/15 px-1.5 text-[11px] font-medium text-info">{activeCount}</span>
          )}
        </button>
        <button className={navItem(route.page === "files")} onClick={() => navigate({ page: "files" })}>
          <FolderOpen className="h-4 w-4" /> 文件
        </button>
        <button className={navItem(route.page === "servers")} onClick={() => navigate({ page: "servers" })}>
          <Server className="h-4 w-4" /> 服务器
          <span className="ml-auto text-[11px] text-faint">
            {nodes.filter((n) => n.online).length}/{nodes.length}
          </span>
        </button>
        <button className={navItem(route.page === "settings")} onClick={() => navigate({ page: "settings" })}>
          <Settings className="h-4 w-4" /> 设置
        </button>
      </div>
      <div className="px-4 pb-1 pt-3 text-[11px] font-medium uppercase tracking-wide text-faint">会话</div>
      <div className="min-h-0 flex-1 space-y-0.5 overflow-y-auto px-2 pb-2">
        {list.length === 0 && <div className="px-2.5 py-2 text-[12px] text-faint">这台服务器上还没有会话</div>}
        {list.map((s) => {
          const active = route.page === "session" && route.id === s.id;
          return (
            <div
              key={s.id}
              role="button"
              tabIndex={0}
              onClick={() => navigate({ page: "session", id: s.id })}
              onKeyDown={(e) => e.key === "Enter" && navigate({ page: "session", id: s.id })}
              className={clsx(
                "group flex cursor-pointer items-start gap-2 rounded-lg px-2.5 py-2 transition",
                active ? "bg-sunken" : "hover:bg-sunken",
              )}
            >
              <div className="mt-0.5">
                <StatusIcon status={s.status || "pending"} className="h-3.5 w-3.5" />
              </div>
              <div className="min-w-0 flex-1">
                <div className={clsx("truncate text-[13px]", active ? "font-medium" : "")}>{s.title}</div>
                <div className="truncate text-[11px] text-faint">
                  {isActive(s.status) && s.activity ? (
                    <span className="text-info">{s.activity}</span>
                  ) : (
                    fmtRelative(s.updatedAt)
                  )}
                </div>
              </div>
              <button
                className="rounded p-1 text-faint hover:text-bad md:invisible md:p-0.5 md:group-hover:visible"
                onClick={(e) => {
                  e.stopPropagation();
                  void remove(s.id, s.title);
                }}
                title="删除会话"
              >
                <Trash2 className="h-3.5 w-3.5" />
              </button>
            </div>
          );
        })}
      </div>
      <div className="border-t border-line px-4 py-2.5 text-[11px] text-faint">
        <div className="flex items-center gap-1.5">
          <span className={clsx("h-1.5 w-1.5 rounded-full", connected ? "bg-ok" : "bg-bad")} />
          {connected ? "实时连接正常" : "连接中断，正在重连…"}
          {info?.authEnabled && (
            <button
              className="ml-auto hover:text-fg"
              title="退出登录"
              onClick={async () => {
                await api.logout().catch(() => undefined);
                location.reload();
              }}
            >
              <LogOut className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
        {info && (
          <div className="mt-0.5 truncate" title={`${info.codexBin} · ${info.codexVersion}`}>
            {info.codexVersion} · AgentHost {info.version}
          </div>
        )}
      </div>
    </aside>
  );
}
