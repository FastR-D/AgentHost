/**
 * AtMention – @path file autocomplete for composer inputs.
 *
 * Attach to any <textarea> via ref:
 *   const { panelEl, handleInput } = useAtMention(ref, nodeId, cwd);
 *
 * Renders a panel *above* the textarea when @ is active.
 * Keyboard: ↑↓ navigate, Enter/Tab accept, Escape dismiss.
 */
import { useCallback, useEffect, useRef, useState } from "react";
import { File, Folder } from "lucide-react";
import { api } from "../api";

// ── types ─────────────────────────────────────────────────────────────────────

interface Entry { name: string; dir: boolean }

interface AtState {
  startIndex: number; // position of '@' in the textarea value
  query: string;      // text after '@'
}

// ── helpers ───────────────────────────────────────────────────────────────────

function parseAt(value: string, cursor: number): AtState | null {
  const before = value.slice(0, cursor);
  // '@' preceded by start-of-string or whitespace
  const m = before.match(/(?:^|[\s\n])@([\S]*)$/);
  if (!m) return null;
  return { startIndex: before.lastIndexOf("@"), query: m[1] };
}

// ── hook ──────────────────────────────────────────────────────────────────────

export function useAtMention(
  textareaRef: React.RefObject<HTMLTextAreaElement>,
  nodeId: string,
  cwd: string,
) {
  const [el, setEl] = useState<HTMLTextAreaElement | null>(null);
  const [atState, setAtState] = useState<AtState | null>(null);
  const [entries, setEntries] = useState<Entry[]>([]);
  const [loading, setLoading] = useState(false);
  const [activeIndex, setActiveIndex] = useState(0);
  const fetchRef = useRef(0);

  // Track when the textarea actually mounts
  useEffect(() => {
    setEl(textareaRef.current);
  });

  // ── recompute atState whenever textarea value/cursor changes ────────────────
  const refresh = useCallback(() => {
    const textarea = textareaRef.current;
    if (!textarea) return;
    const st = parseAt(textarea.value, textarea.selectionStart ?? textarea.value.length);
    setAtState(st);
    if (!st) setEntries([]);
  }, [textareaRef]);

  // ── fetch completions when query changes ────────────────────────────────────
  useEffect(() => {
    if (!atState) { setEntries([]); return; }
    const { query } = atState;
    const slash = query.lastIndexOf("/");
    const dirPart = slash >= 0 ? query.slice(0, slash) : "";
    const prefix  = slash >= 0 ? query.slice(slash + 1) : query;
    const dir = dirPart.startsWith("/")
      ? dirPart
      : [cwd, dirPart].filter(Boolean).join("/");

    const id = ++fetchRef.current;
    setLoading(true);
    api.files(nodeId, dir || cwd)
      .then((list) => {
        if (fetchRef.current !== id) return;
        const filtered = (list.entries ?? [])
          .filter((e) => !prefix || e.name.toLowerCase().startsWith(prefix.toLowerCase()))
          .slice(0, 12)
          .map((e) => ({ name: e.name, dir: e.dir }));
        setEntries(filtered);
        setActiveIndex(0);
      })
      .catch(() => { if (fetchRef.current === id) setEntries([]); })
      .finally(() => { if (fetchRef.current === id) setLoading(false); });
  }, [atState?.query, nodeId, cwd]);

  // ── insert accepted path ────────────────────────────────────────────────────
  const accept = useCallback((name: string, isDir: boolean) => {
    const textarea = textareaRef.current;
    if (!textarea || !atState) return;
    const cursor = textarea.selectionStart ?? textarea.value.length;
    const before = textarea.value.slice(0, atState.startIndex);
    const after  = textarea.value.slice(cursor);
    const slash  = atState.query.lastIndexOf("/");
    const dirPrefix = slash >= 0 ? atState.query.slice(0, slash + 1) : "";
    const inserted = isDir ? `@${dirPrefix}${name}/` : `@${dirPrefix}${name} `;
    const next = before + inserted + after;
    // Write directly into the textarea and fire a native input event so
    // assistant-ui's controlled input picks up the change.
    const nativeSetter = Object.getOwnPropertyDescriptor(
      window.HTMLTextAreaElement.prototype, "value"
    )?.set;
    nativeSetter?.call(textarea, next);
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
    const newPos = before.length + inserted.length;
    requestAnimationFrame(() => {
      textarea.setSelectionRange(newPos, newPos);
      textarea.focus();
      refresh();
    });
  }, [atState, textareaRef, refresh]);

  // ── keyboard handler (call from onKeyDown on the textarea) ──────────────────
  const onKeyDown = useCallback((e: KeyboardEvent) => {
    if (!atState || (entries.length === 0 && !loading)) return;
    if (e.key === "ArrowDown") {
      e.preventDefault(); e.stopPropagation();
      setActiveIndex((i) => Math.min(i + 1, entries.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault(); e.stopPropagation();
      setActiveIndex((i) => Math.max(i - 1, 0));
    } else if ((e.key === "Enter" || e.key === "Tab") && !(e as any).isComposing) {
      const entry = entries[activeIndex];
      if (entry) { e.preventDefault(); e.stopPropagation(); accept(entry.name, entry.dir); }
    } else if (e.key === "Escape") {
      e.preventDefault(); e.stopPropagation();
      setAtState(null); setEntries([]);
    }
  }, [atState, entries, loading, activeIndex, accept]);

  // Attach/detach keydown listener on the textarea
  useEffect(() => {
    if (!el) return;
    el.addEventListener("keydown", onKeyDown, true); // capture so we beat assistant-ui
    return () => el.removeEventListener("keydown", onKeyDown, true);
  }, [el, onKeyDown]);

  // Refresh atState on every input/selectionchange via native listeners
  useEffect(() => {
    if (!el) return;
    const handler = () => setTimeout(refresh, 0); // setTimeout lets DOM settle first
    el.addEventListener("input", handler);
    el.addEventListener("keyup", handler);
    el.addEventListener("click", handler);
    el.addEventListener("selectionchange", handler);
    return () => {
      el.removeEventListener("input", handler);
      el.removeEventListener("keyup", handler);
      el.removeEventListener("click", handler);
      el.removeEventListener("selectionchange", handler);
    };
  }, [el, refresh]);

  const open = !!atState && (loading || entries.length > 0);
  return { open, entries, loading, activeIndex, setActiveIndex, accept, refresh };
}

// ── panel component ───────────────────────────────────────────────────────────

interface PanelProps {
  entries: Entry[];
  loading: boolean;
  activeIndex: number;
  onSelect: (name: string, isDir: boolean) => void;
  onHover: (i: number) => void;
}

export function AtMentionPanel({ entries, loading, activeIndex, onSelect, onHover }: PanelProps) {
  if (!loading && entries.length === 0) return null;
  return (
    <div className="border-t border-line">
      {loading && entries.length === 0 ? (
        <div className="px-3 py-2 text-[12px] text-faint">加载中…</div>
      ) : (
        <ul role="listbox" className="max-h-52 overflow-y-auto py-1">
          {entries.map((e, i) => (
            <li
              key={e.name}
              role="option"
              aria-selected={i === activeIndex}
              className={`flex cursor-pointer items-center gap-2 px-3 py-1.5 text-[13px] select-none
                ${i === activeIndex ? "bg-accent/15 text-fg" : "text-muted hover:bg-panel"}`}
              onMouseEnter={() => onHover(i)}
              onMouseDown={(ev) => { ev.preventDefault(); onSelect(e.name, e.dir); }}
            >
              {e.dir
                ? <Folder className="h-3.5 w-3.5 shrink-0 text-info" />
                : <File   className="h-3.5 w-3.5 shrink-0 text-faint" />}
              <span className="truncate font-mono">{e.name}{e.dir ? "/" : ""}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
