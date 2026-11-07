import { useEffect, useState } from "react";
import clsx from "clsx";
import { Bot, CheckCircle2, GitBranch, KeyRound, Loader2, Play, Save, Settings, XCircle } from "lucide-react";
import { api } from "../api";
import { useStore, guard, toast } from "../store";
import type { CheckResult, SettingsView as SV } from "../types";
import { CopyButton } from "./ServersView";

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <label className="block text-[13px]">
      <span className="text-muted">{label}</span>
      <div className="mt-1">{children}</div>
      {hint && <span className="mt-1 block text-[11.5px] text-faint">{hint}</span>}
    </label>
  );
}

/** Secret input: shows whether a value is stored, never the value itself. */
function Secret({
  set,
  hint,
  value,
  onChange,
  placeholder,
  onClear,
}: {
  set: boolean;
  hint?: string;
  value: string | null;
  onChange: (v: string | null) => void;
  placeholder: string;
  onClear: () => void;
}) {
  if (set && value === null) {
    return (
      <div className="flex flex-wrap items-center gap-2">
        <span className="chip !py-1.5 font-mono">已设置 {hint}</span>
        <button type="button" className="btn !py-1" onClick={() => onChange("")}>
          更换
        </button>
        <button type="button" className="btn !py-1 hover:!text-bad" onClick={onClear}>
          清除
        </button>
      </div>
    );
  }
  return (
    <input
      className="input font-mono"
      type="password"
      autoComplete="off"
      value={value ?? ""}
      placeholder={placeholder}
      onChange={(e) => onChange(e.target.value)}
    />
  );
}

function NodeSelect({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const nodes = useStore((s) => s.nodes);
  if (nodes.length < 2) return null;
  return (
    <select className="input !w-auto !py-1.5" value={value} onChange={(e) => onChange(e.target.value)}>
      {nodes.map((n) => (
        <option key={n.id} value={n.id} disabled={!n.online}>
          在 {n.name} 上测试{n.online ? "" : "（离线）"}
        </option>
      ))}
    </select>
  );
}

function CheckOutput({ res }: { res: CheckResult | null }) {
  if (!res) return null;
  return (
    <div className={clsx("mt-2 rounded-lg px-3 py-2 text-[12.5px]", res.ok ? "bg-ok/10 text-ok" : "bg-bad/10 text-bad")}>
      <div className="flex items-center gap-1.5 font-medium">
        {res.ok ? <CheckCircle2 className="h-4 w-4" /> : <XCircle className="h-4 w-4" />}
        {res.ok ? "成功" : "失败"}
      </div>
      {res.output && <pre className="term mt-1 max-h-48 overflow-auto opacity-90">{res.output}</pre>}
    </div>
  );
}

export function SettingsView() {
  const defaultNode = useStore((s) => s.defaultNode);
  const [v, setV] = useState<SV | null>(null);
  const [codex, setCodex] = useState({ baseUrl: "", wireApi: "", model: "", reasoningEffort: "" });
  const [apiKey, setApiKey] = useState<string | null>(null);
  const [git, setGit] = useState({ tokenUser: "", tokenHost: "", userName: "", userEmail: "" });
  const [token, setToken] = useState<string | null>(null);
  const [privKey, setPrivKey] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [testNode, setTestNode] = useState(defaultNode);
  const [repo, setRepo] = useState("");
  const [busy, setBusy] = useState<"" | "codex" | "git" | "key">("");
  const [codexRes, setCodexRes] = useState<CheckResult | null>(null);
  const [gitRes, setGitRes] = useState<CheckResult | null>(null);

  const apply = (s: SV) => {
    setV(s);
    setCodex({
      baseUrl: s.codex.baseUrl,
      wireApi: s.codex.wireApi,
      model: s.codex.model,
      reasoningEffort: s.codex.reasoningEffort,
    });
    setGit({ tokenUser: s.git.tokenUser, tokenHost: s.git.tokenHost, userName: s.git.userName, userEmail: s.git.userEmail });
    setApiKey(null);
    setToken(null);
    setPrivKey(null);
  };
  useEffect(() => {
    api.settings().then(apply).catch((e) => toast(e.message));
  }, []);
  useEffect(() => setTestNode(defaultNode), [defaultNode]);

  const save = async (extra?: object) => {
    setSaving(true);
    const patch = {
      codex: { ...codex, ...(apiKey !== null ? { apiKey } : {}) },
      git: { ...git, ...(token !== null ? { token } : {}), ...(privKey !== null ? { sshPrivateKey: privKey } : {}) },
      ...extra,
    };
    const res = await guard(api.saveSettings(patch));
    setSaving(false);
    if (res) {
      apply(res);
      toast("已保存，新启动的任务会使用新设置", "info");
    }
    return res;
  };

  if (!v)
    return (
      <div className="flex h-full items-center justify-center text-faint">
        <Loader2 className="h-5 w-5 animate-spin" />
      </div>
    );

  const genKey = async () => {
    if (v.git.sshKeySet && !confirm("生成新密钥会替换现有密钥，之前添加到 GitHub 的公钥将不再可用。继续？")) return;
    setBusy("key");
    const res = await guard(api.generateSSHKey());
    setBusy("");
    if (res) {
      apply(res);
      toast("已生成新密钥，把公钥添加到代码托管平台即可", "info");
    }
  };

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-3xl space-y-5 px-4 py-5 pb-24 md:px-6">
        <div className="flex items-center gap-2">
          <Settings className="h-5 w-5" />
          <h1 className="text-lg font-semibold">设置</h1>
          <span className="text-[12px] text-faint">对所有服务器生效，不需要登录到服务器上配置</span>
        </div>

        <section className="space-y-3 rounded-xl border border-line p-4">
          <div className="flex items-center gap-2 font-medium">
            <Bot className="h-4 w-4 text-info" /> Codex 接口
          </div>
          <p className="text-[12.5px] text-muted">
            留空接口地址则使用每台服务器自己的 Codex 配置（~/.codex）。填写后所有服务器统一使用这里的地址和密钥。
          </p>
          <Field label="接口地址（Base URL）" hint="OpenAI 官方：https://api.openai.com/v1；也可以填兼容 OpenAI 的代理或网关地址">
            <input
              className="input font-mono"
              value={codex.baseUrl}
              placeholder="https://api.openai.com/v1"
              onChange={(e) => setCodex({ ...codex, baseUrl: e.target.value })}
              inputMode="url"
              autoCapitalize="off"
            />
          </Field>
          <Field label="API Key">
            <Secret
              set={v.codex.apiKeySet}
              hint={v.codex.apiKeyHint}
              value={apiKey}
              onChange={setApiKey}
              placeholder="sk-…"
              onClear={() => save({ codex: { ...codex, apiKey: "" } })}
            />
          </Field>
          <div className="grid gap-3 sm:grid-cols-3">
            <Field label="默认模型">
              <input className="input font-mono" value={codex.model} placeholder="留空用默认" onChange={(e) => setCodex({ ...codex, model: e.target.value })} autoCapitalize="off" />
            </Field>
            <Field label="推理强度">
              <select className="input" value={codex.reasoningEffort} onChange={(e) => setCodex({ ...codex, reasoningEffort: e.target.value })}>
                <option value="">默认</option>
                <option value="low">低</option>
                <option value="medium">中</option>
                <option value="high">高</option>
              </select>
            </Field>
            <Field label="接口协议">
              <select className="input" value={codex.wireApi} onChange={(e) => setCodex({ ...codex, wireApi: e.target.value })}>
                <option value="">responses（默认）</option>
                <option value="chat">chat completions</option>
              </select>
            </Field>
          </div>
          <div className="flex flex-wrap items-center gap-2 border-t border-line pt-3">
            <NodeSelect value={testNode} onChange={setTestNode} />
            <button
              className="btn"
              disabled={busy !== ""}
              onClick={async () => {
                setBusy("codex");
                setCodexRes(null);
                if (await save()) setCodexRes((await guard(api.testCodex(testNode))) ?? null);
                setBusy("");
              }}
            >
              {busy === "codex" ? <Loader2 className="h-4 w-4 animate-spin" /> : <Play className="h-4 w-4" />} 保存并测试 Codex
            </button>
          </div>
          <CheckOutput res={codexRes} />
        </section>

        <section className="space-y-3 rounded-xl border border-line p-4">
          <div className="flex items-center gap-2 font-medium">
            <GitBranch className="h-4 w-4 text-info" /> Git 拉取与推送
          </div>
          <div className="rounded-lg bg-panel p-3">
            <div className="flex items-center gap-2 text-[13px] font-medium">
              <KeyRound className="h-4 w-4" /> SSH 密钥（git@… 地址）
              {v.git.sshKeySet && <span className="chip">已配置</span>}
            </div>
            {v.git.sshPublicKey ? (
              <div className="mt-2">
                <div className="mb-1 flex items-center text-[12px] text-faint">
                  公钥：添加到 GitHub 仓库的 Deploy keys（需要推送就勾选 Allow write access），或账号的 SSH keys
                  <span className="ml-auto">
                    <CopyButton text={v.git.sshPublicKey} label="复制公钥" />
                  </span>
                </div>
                <pre className="term overflow-x-auto rounded-lg border border-line bg-bg p-2 text-[11.5px]">{v.git.sshPublicKey}</pre>
              </div>
            ) : (
              v.git.sshKeySet && <div className="mt-1 text-[12px] text-faint">已保存私钥（无法解析出公钥，格式不是 OpenSSH）</div>
            )}
            <div className="mt-2 flex flex-wrap gap-2">
              <button className="btn" onClick={genKey} disabled={busy !== ""}>
                {busy === "key" ? <Loader2 className="h-4 w-4 animate-spin" /> : <KeyRound className="h-4 w-4" />}
                {v.git.sshKeySet ? "重新生成" : "生成新密钥"}
              </button>
              {privKey === null ? (
                <button className="btn" onClick={() => setPrivKey("")}>
                  粘贴已有私钥
                </button>
              ) : null}
              {v.git.sshKeySet && (
                <button className="btn hover:!text-bad" onClick={() => confirm("删除 SSH 密钥？") && save({ git: { ...git, sshPrivateKey: "" } })}>
                  删除
                </button>
              )}
            </div>
            {privKey !== null && (
              <textarea
                className="input mt-2 h-32 font-mono text-[12px]"
                placeholder={"-----BEGIN OPENSSH PRIVATE KEY-----\n…\n-----END OPENSSH PRIVATE KEY-----"}
                value={privKey}
                onChange={(e) => setPrivKey(e.target.value)}
                spellCheck={false}
              />
            )}
          </div>

          <div className="rounded-lg bg-panel p-3">
            <div className="text-[13px] font-medium">HTTPS 访问令牌（https://… 地址）</div>
            <div className="mt-2 grid gap-3 sm:grid-cols-2">
              <Field label="访问令牌" hint="GitHub：Settings → Developer settings → Personal access tokens">
                <Secret
                  set={v.git.tokenSet}
                  hint={v.git.tokenHint}
                  value={token}
                  onChange={setToken}
                  placeholder="ghp_… / glpat-…"
                  onClear={() => save({ git: { ...git, token: "" } })}
                />
              </Field>
              <Field label="仅用于这个主机" hint="留空则所有 https 仓库都用这个令牌">
                <input className="input font-mono" value={git.tokenHost} placeholder="github.com" onChange={(e) => setGit({ ...git, tokenHost: e.target.value })} autoCapitalize="off" />
              </Field>
              <Field label="用户名" hint="GitHub 可留空；GitLab 填 oauth2；Gitee 填账号">
                <input className="input font-mono" value={git.tokenUser} placeholder="x-access-token" onChange={(e) => setGit({ ...git, tokenUser: e.target.value })} autoCapitalize="off" />
              </Field>
            </div>
          </div>

          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="提交者名字">
              <input className="input" value={git.userName} placeholder="AgentHost" onChange={(e) => setGit({ ...git, userName: e.target.value })} />
            </Field>
            <Field label="提交者邮箱">
              <input className="input" type="email" value={git.userEmail} placeholder="bot@example.com" onChange={(e) => setGit({ ...git, userEmail: e.target.value })} autoCapitalize="off" />
            </Field>
          </div>

          <div className="space-y-2 border-t border-line pt-3">
            <Field label="测试一个仓库">
              <input
                className="input font-mono"
                value={repo}
                placeholder="git@github.com:me/repo.git 或 https://github.com/me/repo.git"
                onChange={(e) => setRepo(e.target.value)}
                autoCapitalize="off"
              />
            </Field>
            <div className="flex flex-wrap items-center gap-2">
              <NodeSelect value={testNode} onChange={setTestNode} />
              <button
                className="btn"
                disabled={busy !== "" || !repo.trim()}
                onClick={async () => {
                  setBusy("git");
                  setGitRes(null);
                  if (await save()) setGitRes((await guard(api.testGit(testNode, repo))) ?? null);
                  setBusy("");
                }}
              >
                {busy === "git" ? <Loader2 className="h-4 w-4 animate-spin" /> : <Play className="h-4 w-4" />} 保存并测试拉取
              </button>
            </div>
            <CheckOutput res={gitRes} />
          </div>
        </section>
      </div>

      <div className="fixed inset-x-0 bottom-0 z-20 border-t border-line bg-bg/95 px-4 py-3 pb-safe backdrop-blur md:static md:mx-auto md:max-w-3xl md:border-0 md:bg-transparent md:px-6 md:pt-0">
        <button className="btn-primary w-full justify-center !py-2.5 md:w-auto" onClick={() => save()} disabled={saving}>
          {saving ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />} 保存设置
        </button>
      </div>
    </div>
  );
}
