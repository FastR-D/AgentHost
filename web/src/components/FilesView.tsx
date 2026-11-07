import { useEffect, useMemo, useState } from "react";
import clsx from "clsx";
import {
  ArrowLeft,
  ArrowUp,
  ChevronRight,
  Download,
  Eye,
  EyeOff,
  File,
  FileCode2,
  FileImage,
  FileText,
  Folder,
  FolderOpen,
  Loader2,
  Play,
  RefreshCw,
  WrapText,
} from "lucide-react";
import { api } from "../api";
import { guard, navigate, nodeName, setCurrentNode, useCurrentNode } from "../store";
import type { FileList, FileView as FV } from "../types";
import { fmtRelative } from "../lib/format";
import { Markdown } from "./Markdown";
import { Sheet, WhereChip } from "./DirPicker";
import { ParallelChip } from "./Parallel";

export function fmtSize(n: number) {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(n < 10240 ? 1 : 0)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

const codeExt = /\.(go|ts|tsx|js|jsx|py|rs|java|kt|c|h|cpp|cs|rb|php|sh|bash|zsh|sql|json|ya?ml|toml|xml|html|css|scss|vue|swift|lua|ini|conf|env|dockerfile|mk)$/i;
const imageExt = /\.(png|jpe?g|gif|webp|bmp|ico|avif)$/i;

function fileIcon(name: string, dir: boolean) {
  if (dir) return <Folder className="h-4 w-4 shrink-0 text-info" />;
  if (imageExt.test(name)) return <FileImage className="h-4 w-4 shrink-0 text-warn" />;
  if (/\.(md|markdown|txt|log)$/i.test(name)) return <FileText className="h-4 w-4 shrink-0 text-muted" />;
  if (codeExt.test(name)) return <FileCode2 className="h-4 w-4 shrink-0 text-ok" />;
  return <File className="h-4 w-4 shrink-0 text-faint" />;
}

const join = (dir: string, name: string) => `${dir.replace(/\/$/, "")}/${name}`;

function Breadcrumb({ path, onGo }: { path: string; onGo: (p: string) => void }) {
  const parts = path.split("/").filter(Boolean);
  return (
    <div className="flex min-w-0 items-center overflow-x-auto whitespace-nowrap font-mono text-[12.5px] [scrollbar-width:none]">
      <button className="rounded px-1 py-0.5 text-muted hover:bg-sunken hover:text-fg" onClick={() => onGo("/")}>
        /
      </button>
      {parts.map((p, i) => (
        <span key={i} className="flex items-center">
          {i > 0 && <ChevronRight className="h-3 w-3 shrink-0 text-faint" />}
          <button
            className={clsx("rounded px-1 py-0.5 hover:bg-sunken", i === parts.length - 1 ? "font-medium text-fg" : "text-muted hover:text-fg")}
            onClick={() => onGo("/" + parts.slice(0, i + 1).join("/"))}
          >
            {p}
          </button>
        </span>
      ))}
    </div>
  );
}

const dirOf = (p: string) => p.slice(0, p.lastIndexOf("/")) || "/";

/** Run a Markdown file from the code as a task, in its own directory. */
function RunFileSheet({ node, path, onClose }: { node: string; path: string; onClose: () => void }) {
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [parallel, setParallel] = useState(1);
  const dir = dirOf(path);
  const run = async () => {
    setBusy(true);
    const res = await guard(api.startTask({ taskFile: path, nodeId: node, cwd: dir, text: note.trim(), parallel }));
    setBusy(false);
    if (res) navigate({ page: "session", id: res.session.id });
  };
  return (
    <Sheet
      title={
        <span className="flex items-center gap-2">
          <Play className="h-4 w-4 text-info" /> 作为任务运行
        </span>
      }
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            取消
          </button>
          <button className="btn-primary" onClick={run} disabled={busy}>
            {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : <Play className="h-4 w-4" />} 开始
          </button>
        </>
      }
    >
      <div className="space-y-3 p-4">
        <div className="text-[13.5px]">
          让 Codex 按 <span className="font-mono font-medium">{path.split("/").pop()}</span> 里写的去做。
        </div>
        <div className="text-[12.5px] text-muted">
          在 <span className="break-all font-mono">{dir}</span> 新开一个会话运行
        </div>
        <textarea
          className="input min-h-[72px] resize-none"
          value={note}
          onChange={(e) => setNote(e.target.value)}
          placeholder="补充说明（可选），会接在文件内容后面"
        />
        <ParallelChip value={parallel} onChange={setParallel} />
      </div>
    </Sheet>
  );
}

function Preview({ node, path, onBack }: { node: string; path: string; onBack: () => void }) {
  const [v, setV] = useState<FV | null>(null);
  const [err, setErr] = useState("");
  const [wrap, setWrap] = useState(true);
  const isMd = /\.(md|markdown)$/i.test(path);
  // Show the real file first; Markdown rendering is one tap away.
  const [rendered, setRendered] = useState(false);
  const [runOpen, setRunOpen] = useState(false);
  useEffect(() => {
    setV(null);
    setErr("");
    setRendered(false);
    api.fileContent(node, path).then(setV).catch((e) => setErr(e.message));
  }, [node, path]);
  const lines = useMemo(() => (v?.text ?? "").replace(/\n$/, "").split("\n"), [v?.text]);
  const name = path.split("/").pop();
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-center gap-2 border-b border-line px-3 py-2">
        <button className="btn-ghost !p-1.5 md:hidden" onClick={onBack} aria-label="返回">
          <ArrowLeft className="h-5 w-5" />
        </button>
        {fileIcon(path, false)}
        <div className="min-w-0 flex-1">
          <div className="truncate text-[13.5px] font-medium">{name}</div>
          {v && (
            <div className="truncate text-[11px] text-faint">
              {fmtSize(v.size)} · {fmtRelative(v.modTime)}
              {v.truncated ? " · 只显示前 1 MB" : ""}
            </div>
          )}
        </div>
        {v?.kind === "text" && isMd && (
          <button className="btn-primary shrink-0 !px-2 !py-1 text-[12px]" onClick={() => setRunOpen(true)} title="把这个文件当作任务交给 Codex">
            <Play className="h-3.5 w-3.5" /> <span className="md:hidden">运行</span>
            <span className="hidden md:inline">作为任务运行</span>
          </button>
        )}
        {v?.kind === "text" && isMd && (
          <button className="btn shrink-0 !px-2 !py-1 text-[12px]" onClick={() => setRendered(!rendered)}>
            {rendered ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />} {rendered ? "源码" : "预览"}
          </button>
        )}
        {v?.kind === "text" && !rendered && (
          <button className={clsx("btn-ghost !p-1.5", wrap && "text-fg")} onClick={() => setWrap(!wrap)} title="自动换行">
            <WrapText className="h-4 w-4" />
          </button>
        )}
        <a className="btn-ghost !p-1.5" href={api.rawFileURL(node, path, true)} title="下载">
          <Download className="h-4 w-4" />
        </a>
      </div>
      <div className="min-h-0 flex-1 overflow-auto">
        {err && <div className="p-4 text-[13px] text-bad">{err}</div>}
        {!v && !err && (
          <div className="flex items-center gap-2 p-4 text-[13px] text-faint">
            <Loader2 className="h-4 w-4 animate-spin" /> 加载中…
          </div>
        )}
        {v?.kind === "image" && (
          <div className="flex justify-center p-4">
            <img src={api.rawFileURL(node, path)} alt={name} className="max-h-[75dvh] max-w-full rounded-lg border border-line" />
          </div>
        )}
        {v?.kind === "binary" && (
          <div className="p-6 text-center text-[13px] text-muted">
            二进制文件（{fmtSize(v.size)}），无法预览。
            <div className="mt-3">
              <a className="btn inline-flex" href={api.rawFileURL(node, path, true)}>
                <Download className="h-4 w-4" /> 下载
              </a>
            </div>
          </div>
        )}
        {v?.kind === "text" &&
          (rendered ? (
            <div className="px-4 py-3">
              <Markdown text={v.text ?? ""} />
            </div>
          ) : (
            <table className="w-full border-collapse font-mono text-[12px] leading-[1.6]">
              <tbody>
                {lines.map((l, i) => (
                  <tr key={i} className="align-top">
                    <td className="select-none border-r border-line bg-panel px-2 text-right tabular-nums text-faint">{i + 1}</td>
                    <td className={clsx("px-3", wrap ? "whitespace-pre-wrap break-all" : "whitespace-pre")}>{l || " "}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ))}
      </div>
      {runOpen && <RunFileSheet node={node} path={path} onClose={() => setRunOpen(false)} />}
    </div>
  );
}

/** Read-only file browser on any server, to check what a task produced. */
export function FilesView({ node, path, file }: { node?: string; path?: string; file?: string }) {
  const current = useCurrentNode();
  const nd = node || current;
  // A link to another server's files makes it the current server.
  useEffect(() => {
    if (node) setCurrentNode(node);
  }, [node]);
  const [list, setList] = useState<FileList | null>(null);
  const [err, setErr] = useState("");
  const [loading, setLoading] = useState(false);
  const [hidden, setHidden] = useState(false);

  const go = (p: string, f?: string) => navigate({ page: "files", node: nd, path: p, file: f });
  const load = () => {
    setLoading(true);
    setErr("");
    api
      .files(nd, path ?? "")
      .then((l) => {
        setList(l);
        if (!path) navigate({ page: "files", node: nd, path: l.path, file });
      })
      .catch((e) => setErr(e.message))
      .finally(() => setLoading(false));
  };
  useEffect(load, [nd, path]);

  const entries = (list?.entries ?? []).filter((e) => hidden || !e.name.startsWith("."));
  const hiddenCount = (list?.entries.length ?? 0) - entries.length;

  return (
    <div className="flex h-full min-h-0">
      <div className={clsx("flex min-w-0 flex-col border-line md:w-[380px] md:shrink-0 md:border-r", file ? "hidden md:flex" : "flex w-full")}>
        <div className="space-y-2 border-b border-line px-3 py-2.5">
          <div className="flex items-center gap-2">
            <FolderOpen className="h-4 w-4 shrink-0 text-info" />
            <span className="font-medium">文件</span>
            <div className="min-w-0">
              <WhereChip node={nd} path={list?.path} onChange={(p) => navigate({ page: "files", node: nd, path: p })} />
            </div>
          </div>
          <div className="flex items-center gap-1">
            <button className="btn-ghost !p-1.5" disabled={!list || list.parent === list.path} onClick={() => list && go(list.parent)} title="上一级">
              <ArrowUp className="h-4 w-4" />
            </button>
            {list && <Breadcrumb path={list.path} onGo={(p) => go(p)} />}
            <div className="ml-auto flex shrink-0 items-center">
              <button className={clsx("btn-ghost !p-1.5", hidden && "text-fg")} onClick={() => setHidden(!hidden)} title="显示隐藏文件">
                {hidden ? <Eye className="h-4 w-4" /> : <EyeOff className="h-4 w-4" />}
              </button>
              <button className="btn-ghost !p-1.5" onClick={load} title="刷新">
                <RefreshCw className={clsx("h-4 w-4", loading && "animate-spin")} />
              </button>
            </div>
          </div>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto">
          {err && <div className="p-4 text-[13px] text-bad">{err}</div>}
          {!err && list && entries.length === 0 && <div className="p-4 text-[13px] text-faint">空目录</div>}
          {entries.map((e) => {
            const p = join(list!.path, e.name);
            return (
              <button
                key={e.name}
                onClick={() => (e.dir ? go(p) : go(list!.path, p))}
                className={clsx(
                  "flex w-full items-center gap-2.5 border-b border-line/50 px-3 py-2.5 text-left hover:bg-sunken md:py-2",
                  file === p && "bg-sunken",
                )}
              >
                {fileIcon(e.name, e.dir)}
                <span className="min-w-0 flex-1 truncate text-[14px] md:text-[13px]">
                  {e.name}
                  {e.link && <span className="ml-1 text-faint">→</span>}
                </span>
                <span className="shrink-0 text-right text-[11px] text-faint">
                  {!e.dir && <span className="mr-2 tabular-nums">{fmtSize(e.size)}</span>}
                  {fmtRelative(e.modTime)}
                </span>
              </button>
            );
          })}
          {hiddenCount > 0 && (
            <button className="w-full px-3 py-2 text-left text-[12px] text-faint hover:text-fg" onClick={() => setHidden(true)}>
              还有 {hiddenCount} 个隐藏文件…
            </button>
          )}
        </div>
        <div className="border-t border-line px-3 py-1.5 text-[11px] text-faint">只读 · {nodeName(nd)}</div>
      </div>
      <div className={clsx("min-w-0 flex-1", file ? "block" : "hidden md:block")}>
        {file ? (
          <Preview node={nd} path={file} onBack={() => go(list?.path ?? path ?? "")} />
        ) : (
          <div className="flex h-full items-center justify-center text-[13px] text-faint">选择一个文件查看内容</div>
        )}
      </div>
    </div>
  );
}
