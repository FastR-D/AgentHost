import { useEffect, useState } from "react";
import clsx from "clsx";
import { ChevronDown, KeyRound, Loader2, Menu, Plus } from "lucide-react";
import { api, setUnauthorizedHandler } from "./api";
import { useStore, connect, disconnect, nodeName, parseHash, resync, useCurrentNode, type Route } from "./store";
import { Sidebar } from "./components/Sidebar";
import { HomeView } from "./components/HomeView";
import { ChatView } from "./components/ChatView";
import { RunsView } from "./components/RunsView";
import { TaskLibView } from "./components/TaskLibView";
import { LogsPanel } from "./components/LogsPanel";
import { FilesView } from "./components/FilesView";
import { ServersView } from "./components/ServersView";
import { SettingsView } from "./components/SettingsView";
import { navigate } from "./store";
import { ServerSheet } from "./components/ServerSwitcher";

function useRoute(): Route {
  const [route, setRoute] = useState(() => parseHash(location.hash));
  useEffect(() => {
    const on = () => setRoute(parseHash(location.hash));
    window.addEventListener("hashchange", on);
    return () => window.removeEventListener("hashchange", on);
  }, []);
  return route;
}

function Login({ onDone }: { onDone: () => void }) {
  const [token, setToken] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      await api.login(token);
      onDone();
    } catch (e: any) {
      setErr(e.message);
    }
    setBusy(false);
  };
  return (
    <div className="flex h-full items-center justify-center px-4">
      <form onSubmit={submit} className="w-full max-w-sm rounded-2xl border border-line bg-bg p-6 shadow-sm">
        <div className="mb-4 flex items-center gap-2">
          <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-accent font-bold text-bg">A</div>
          <div>
            <div className="font-semibold">AgentHost</div>
            <div className="text-[12px] text-faint">输入访问令牌登录</div>
          </div>
        </div>
        <div className="flex items-center gap-2 rounded-lg border border-line bg-bg px-3 focus-within:border-faint">
          <KeyRound className="h-4 w-4 shrink-0 text-faint" />
          <input
            autoFocus
            type="password"
            className="w-full bg-transparent py-2 font-mono text-[13px] text-fg outline-none placeholder:text-faint"
            style={{ fontSize: undefined }}
            placeholder="访问令牌"
            value={token}
            onChange={(e) => setToken(e.target.value)}
          />
        </div>
        {err && <div className="mt-2 text-[12px] text-bad">{err}</div>}
        <button className="btn-primary mt-4 w-full justify-center" disabled={!token || busy}>
          {busy && <Loader2 className="h-4 w-4 animate-spin" />} 登录
        </button>
        <div className="mt-3 text-[11.5px] leading-relaxed text-faint">
          令牌在服务启动日志中打印，也保存在数据目录的 <code>token</code> 文件里；可通过环境变量 AGENTHOST_TOKEN 指定。
        </div>
      </form>
    </div>
  );
}

function Toasts() {
  const toasts = useStore((s) => s.toasts);
  return (
    <div className="pointer-events-none fixed bottom-4 right-4 z-50 flex flex-col gap-2">
      {toasts.map((t) => (
        <div
          key={t.id}
          className={clsx(
            "pointer-events-auto max-w-sm rounded-lg border px-3 py-2 text-[13px] shadow-lg",
            t.kind === "error" ? "border-bad/30 bg-bg text-bad" : "border-line bg-bg text-fg",
          )}
        >
          {t.text}
        </div>
      ))}
    </div>
  );
}

const pageTitle: Record<string, string> = {
  home: "新任务",
  tasks: "任务库",
  runs: "运行记录",
  files: "文件",
  servers: "服务器",
  settings: "设置",
};

/** Top bar on phones: menu, current page / session and server, new task. */
function MobileBar({ route, onMenu }: { route: Route; onMenu: () => void }) {
  const session = useStore((s) => (route.page === "session" ? s.sessions[route.id] : undefined));
  const connected = useStore((s) => s.connected);
  const current = useCurrentNode();
  const online = useStore((s) => s.nodes.find((n) => n.id === current)?.online);
  const [servers, setServers] = useState(false);
  return (
    <div className="flex items-center gap-1 border-b border-line bg-bg px-1.5 py-1 pt-safe md:hidden">
      <button className="btn-ghost !p-2.5" onClick={onMenu} aria-label="菜单">
        <Menu className="h-5 w-5" />
      </button>
      <button className="min-w-0 flex-1 text-center" onClick={() => setServers(true)} title="切换服务器">
        <div className="truncate text-[15px] font-medium leading-tight">
          {route.page === "session" ? session?.title ?? "会话" : pageTitle[route.page]}
        </div>
        <div className="flex items-center justify-center gap-1 text-[11px] leading-tight text-faint">
          <span className={clsx("h-1.5 w-1.5 rounded-full", online ? "bg-ok" : "bg-faint")} />
          <span className="truncate">{nodeName(current)}</span>
          <ChevronDown className="h-3 w-3 shrink-0" />
        </div>
      </button>
      {servers && <ServerSheet onClose={() => setServers(false)} />}
      {!connected && <span className="mr-1 h-2 w-2 rounded-full bg-bad" title="连接中断" />}
      <button className="btn-ghost !p-2.5" onClick={() => navigate({ page: "home" })} aria-label="新任务">
        <Plus className="h-5 w-5" />
      </button>
    </div>
  );
}

export default function App() {
  const [auth, setAuth] = useState<"checking" | "need" | "ok">("checking");
  const route = useRoute();
  const [drawer, setDrawer] = useState(false);
  // Close the drawer whenever the page changes.
  const routeKey = JSON.stringify(route);
  useEffect(() => setDrawer(false), [routeKey]);

  const check = async () => {
    try {
      const a = await api.auth();
      if (a.authenticated) {
        setAuth("ok");
        connect();
        await resync().catch(() => undefined);
      } else setAuth("need");
    } catch {
      setAuth("need");
    }
  };

  useEffect(() => {
    setUnauthorizedHandler(() => {
      disconnect();
      setAuth("need");
    });
    void check();
    return () => disconnect();
  }, []);

  if (auth === "checking")
    return (
      <div className="flex h-full items-center justify-center text-faint">
        <Loader2 className="h-5 w-5 animate-spin" />
      </div>
    );
  if (auth === "need") return <Login onDone={check} />;

  return (
    <div className="flex h-full">
      <Sidebar route={route} className="hidden md:flex" />
      {drawer && (
        <div className="fixed inset-0 z-40 md:hidden" onClick={() => setDrawer(false)}>
          <div className="absolute inset-0 bg-black/30" />
          <div className="absolute inset-y-0 left-0 shadow-xl pt-safe" onClick={(e) => e.stopPropagation()}>
            <Sidebar route={route} />
          </div>
        </div>
      )}
      <div className="flex min-w-0 flex-1 flex-col">
        <MobileBar route={route} onMenu={() => setDrawer(true)} />
        <main className="min-h-0 min-w-0 flex-1">
          {route.page === "home" && <HomeView />}
          {route.page === "session" && <ChatView key={route.id} sessionId={route.id} />}
          {route.page === "runs" && <RunsView />}
          {route.page === "tasks" && <TaskLibView name={route.name} />}
          {route.page === "files" && <FilesView node={route.node} path={route.path} file={route.file} />}
          {route.page === "servers" && <ServersView />}
          {route.page === "settings" && <SettingsView />}
        </main>
      </div>
      <LogsPanel />
      <Toasts />
    </div>
  );
}
