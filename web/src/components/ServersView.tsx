import { useEffect, useState } from "react";
import clsx from "clsx";
import { Check, Copy, Eye, EyeOff, Plus, Server, Trash2 } from "lucide-react";
import { api } from "../api";
import { useStore, guard, toast, useCurrentNode } from "../store";
import { switchServer } from "./ServerSwitcher";
import { fmtRelative } from "../lib/format";

export function CopyButton({ text, label = "复制" }: { text: string; label?: string }) {
  const [done, setDone] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      // Plain-HTTP pages on phones have no clipboard API: fall back to a selection copy.
      const ta = document.createElement("textarea");
      ta.value = text;
      document.body.appendChild(ta);
      ta.select();
      document.execCommand("copy");
      ta.remove();
    }
    setDone(true);
    setTimeout(() => setDone(false), 1500);
  };
  return (
    <button className="btn !py-1" onClick={copy}>
      {done ? <Check className="h-3.5 w-3.5 text-ok" /> : <Copy className="h-3.5 w-3.5" />} {done ? "已复制" : label}
    </button>
  );
}

function JoinGuide() {
  const [join, setJoin] = useState<{ hub: string; token: string } | null>(null);
  const [hub, setHub] = useState("");
  const [name, setName] = useState("server-2");
  const [show, setShow] = useState(false);
  useEffect(() => {
    api.nodeJoin().then((j) => {
      setJoin(j);
      setHub(j.hub);
    });
  }, []);
  if (!join) return null;
  const tok = show ? join.token : join.token.slice(0, 4) + "…" + join.token.slice(-4);
  const script = `./start.sh node --hub ${hub} --token ${join.token} --name ${name}`;
  const version = "v0.2.0";
  const releaseUrl = `https://github.com/FastR-D/AgentHost/releases/download/${version}/agenthost-linux-amd64.tar.gz`;
  const scriptDeploy = `curl -L ${releaseUrl} | tar -xz && cd agenthost-linux-amd64 && ./start.sh node --deploy --daemon --hub ${hub} --token ${join.token} --name ${name}`;
  const docker = `docker run -d --name agenthost-node --restart unless-stopped \\
  -e AGENTHOST_HUB=${hub} -e AGENTHOST_NODE_TOKEN=${join.token} -e AGENTHOST_NODE_NAME=${name} \\
  -v $PWD/workspace:/workspace -v $PWD/node-data:/data \\
  agenthost ./start.sh node`;
  return (
    <div className="rounded-xl border border-line bg-panel p-4">
      <div className="flex items-center gap-2 font-medium">
        <Plus className="h-4 w-4" /> 添加服务器
      </div>
      <p className="mt-1 text-[13px] text-muted">
        在要跑任务的机器上运行下面的命令。它会主动连到这台 Web 服务器，所以那台机器在内网、没有公网地址、不开端口都可以；只要它能访问下面的地址。
      </p>
      <div className="mt-3 grid gap-2 sm:grid-cols-2">
        <label className="text-[12px] text-muted">
          它访问本机用的地址
          <input className="input mt-1 font-mono" value={hub} onChange={(e) => setHub(e.target.value)} />
        </label>
        <label className="text-[12px] text-muted">
          服务器名称
          <input className="input mt-1 font-mono" value={name} onChange={(e) => setName(e.target.value.replace(/\s+/g, "-"))} />
        </label>
      </div>
      <div className="mt-2 flex flex-wrap items-center gap-2 text-[12px] text-muted">
        节点令牌 <code className="font-mono text-fg">{tok}</code>
        <button className="btn-ghost !p-1" onClick={() => setShow(!show)} title={show ? "隐藏" : "显示"}>
          {show ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
        </button>
        <span className="text-faint">（与登录令牌不同，只用于服务器接入）</span>
      </div>
      <div className="mt-3 space-y-3">
        <div>
          <div className="mb-1 flex items-center text-[12px] text-faint">
            Release 包（推荐）：下载解压后直接启动，后台运行
            <span className="ml-auto">
              <CopyButton text={scriptDeploy} />
            </span>
          </div>
          <pre className="term overflow-x-auto rounded-lg border border-line bg-bg p-2.5">{scriptDeploy.replace(join.token, tok)}</pre>
        </div>
        <div>
          <div className="mb-1 flex items-center text-[12px] text-faint">
            源码目录（会自动安装 Codex 等依赖，前台运行）
            <span className="ml-auto">
              <CopyButton text={script} />
            </span>
          </div>
          <pre className="term overflow-x-auto rounded-lg border border-line bg-bg p-2.5">{script.replace(join.token, tok)}</pre>
        </div>
        <div>
          <div className="mb-1 flex items-center text-[12px] text-faint">
            或用 Docker（先在那台机器上构建 agenthost 镜像）
            <span className="ml-auto">
              <CopyButton text={docker} />
            </span>
          </div>
          <pre className="term overflow-x-auto rounded-lg border border-line bg-bg p-2.5">{docker.replace(join.token, tok)}</pre>
        </div>
        <p className="text-[12px] text-faint">
          Codex 的接口地址和密钥、Git 密钥在“设置”里统一配置，会自动用到每台服务器上，不需要在那台机器上登录 Codex。
        </p>
      </div>
    </div>
  );
}

export function ServersView() {
  const nodes = useStore((s) => s.nodes);
  const current = useCurrentNode();
  useEffect(() => {
    api.nodes().then((r) => useStore.setState({ nodes: r.nodes, defaultNode: r.default }));
  }, []);
  const forget = async (id: string, name: string) => {
    if (!confirm(`从列表中移除服务器「${name}」？它重新连接时会再出现。`)) return;
    if (await guard(api.forgetNode(id))) toast("已移除", "info");
  };
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-4xl space-y-4 px-4 py-5 md:px-6">
        <div className="flex items-center gap-2">
          <Server className="h-5 w-5" />
          <h1 className="text-lg font-semibold">服务器</h1>
          <span className="text-[12px] text-faint">{nodes.filter((n) => n.online).length} 台在线</span>
        </div>
        <div className="grid gap-3 md:grid-cols-2">
          {nodes.map((n) => (
            <div key={n.id} className={clsx("rounded-xl border p-4", n.online ? "border-line" : "border-dashed border-line opacity-70")}>
              <div className="flex items-center gap-2">
                <span className={clsx("h-2.5 w-2.5 rounded-full", n.online ? "bg-ok" : "bg-faint")} />
                <span className="font-medium">{n.name}</span>
                {n.local && <span className="chip">Web 服务器本机</span>}
                {n.id === current && <span className="chip !border-info/40 !text-info">当前</span>}
                <span className="ml-auto text-[12px] text-faint">
                  {n.online ? (n.procs ? `${n.procs} 个进程运行中` : "空闲") : `离线 · ${fmtRelative(n.lastSeen)}`}
                </span>
              </div>
              <dl className="mt-2 grid grid-cols-[72px_1fr] gap-x-2 gap-y-1 text-[12.5px]">
                <dt className="text-faint">主机</dt>
                <dd className="truncate">
                  {n.hostname || "—"} {n.os && <span className="text-faint">· {n.os}/{n.arch}</span>}
                </dd>
                <dt className="text-faint">工作区</dt>
                <dd className="truncate font-mono">{n.workspace || "—"}</dd>
                <dt className="text-faint">Codex</dt>
                <dd className={clsx("truncate", n.codexVersion === "unavailable" && "text-bad")}>
                  {n.codexVersion === "unavailable" ? "未安装" : n.codexVersion || "—"}
                </dd>
                {!n.local && (
                  <>
                    <dt className="text-faint">来源</dt>
                    <dd className="truncate">
                      {n.addr || "—"} {n.connectedAt && n.online && <span className="text-faint">· {fmtRelative(n.connectedAt)}连接</span>}
                    </dd>
                  </>
                )}
              </dl>
              <div className="mt-3 flex gap-2 empty:hidden">
                {n.id !== current && (
                  <button className="btn !py-1" onClick={() => switchServer(n.id)}>
                    <Check className="h-3.5 w-3.5" /> 切换到这台
                  </button>
                )}
                {!n.online && !n.local && (
                  <button className="btn !py-1 hover:!text-bad" onClick={() => forget(n.id, n.name)}>
                    <Trash2 className="h-3.5 w-3.5" /> 移除
                  </button>
                )}
              </div>
            </div>
          ))}
        </div>
        <JoinGuide />
      </div>
    </div>
  );
}
