/**
 * AtMention – @path file autocomplete for composer inputs.
 *
 * Key insight: compute atQuery synchronously inside handleChange so cursor
 * and value are never stale (they're the same event's data).
 */
import { useEffect, useRef, useState } from "react";
import { File, Folder } from "lucide-react";
import { api } from "../api";

export interface Entry { name: string; dir: boolean }

interface AtState { startIndex: number; query: string }

function parseAt(value: string, cursor: number): AtState | null {
  const before = value.slice(0, cursor);
  // match '@' at start or after whitespace, followed by non-whitespace
  const m = before.match(/(?:^|[\s\n])@(\S*)$/);
  if (!m) return null;
  return { startIndex: before.lastIndexOf("@"), query: m[1] };
}

export function useAtMention(nodeId: string, cwd: string) {
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  // value/cursor stored as a ref for accept(), state for rendering
  const valueRef = useRef("");
  const cursorRef = useRef(0);

  const [atState, setAtState] = useState<AtState | null>(null);
  const [entries, setEntries] = useState<Entry[]>([]);
  const [loading, setLoading] = useState(false);
  const [activeIndex, setActiveIndex] = useState(0);
  const fetchRef = useRef(0);

  // ── fetch when query changes ──────────────────────────────────────────────
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
        setEntries(
          (list.entries ?? [])
            .filter((e) => !prefix || e.name.toLowerCase().startsWith(prefix.toLowerCase()))
            .slice(0, 12)
            .map((e) => ({ name: e.name, dir: e.dir }))
        );
        setActiveIndex(0);
      })
      .catch(() => { if (fetchRef.current === id) setEntries([]); })
      .finally(() => { if (fetchRef.current === id) setLoading(false); });
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [atState?.query, nodeId, cwd]);

  // ── onChange: compute atState synchronously from the same event ──────────
  const handleChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    const val = e.target.value;
    const cur = e.target.selectionStart ?? val.length;
    valueRef.current = val;
    cursorRef.current = cur;
    setAtState(parseAt(val, cur));
  };

  // also catch cursor movement without text change (click, arrow keys)
  const handleSelect = (e: React.SyntheticEvent<HTMLTextAreaElement>) => {
    const el = e.currentTarget;
    const cur = el.selectionStart ?? el.value.length;
    if (cur === cursorRef.current) return;
    cursorRef.current = cur;
    setAtState(parseAt(valueRef.current, cur));
  };

  // ── accept: insert path ───────────────────────────────────────────────────
  const accept = (name: string, isDir: boolean) => {
    const el = textareaRef.current;
    const st = atState;
    if (!el || !st) return;
    const slash = st.query.lastIndexOf("/");
    const dirPrefix = slash >= 0 ? st.query.slice(0, slash + 1) : "";
    const inserted = isDir ? `@${dirPrefix}${name}/` : `@${dirPrefix}${name} `;
    const before = valueRef.current.slice(0, st.startIndex);
    const after  = valueRef.current.slice(cursorRef.current);
    const next   = before + inserted + after;
    const newPos = before.length + inserted.length;

    // bypass React controlled input
    const nativeSetter = Object.getOwnPropertyDescriptor(
      window.HTMLTextAreaElement.prototype, "value"
    )?.set;
    nativeSetter?.call(el, next);
    el.dispatchEvent(new Event("input", { bubbles: true }));

    valueRef.current = next;
    cursorRef.current = newPos;
    // if dir, keep panel open for further navigation; else close
    setAtState(isDir ? parseAt(next, newPos) : null);

    requestAnimationFrame(() => {
      el.setSelectionRange(newPos, newPos);
      el.focus();
    });
  };

  // ── keyboard handler ──────────────────────────────────────────────────────
  const open = !!atState && (loading || entries.length > 0);

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (!open) return;
    if (e.key === "ArrowDown") {
      e.preventDefault(); e.stopPropagation();
      setActiveIndex((i) => Math.min(i + 1, entries.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault(); e.stopPropagation();
      setActiveIndex((i) => Math.max(i - 1, 0));
    } else if ((e.key === "Enter" || e.key === "Tab") && !e.nativeEvent.isComposing) {
      const entry = entries[activeIndex];
      if (entry) { e.preventDefault(); e.stopPropagation(); accept(entry.name, entry.dir); }
    } else if (e.key === "Escape") {
      e.preventDefault(); e.stopPropagation();
      setAtState(null); setEntries([]);
    }
  };

  return { textareaRef, open, entries, loading, activeIndex, setActiveIndex, accept, handleChange, handleSelect, handleKeyDown };
}

// ── panel ─────────────────────────────────────────────────────────────────────

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
              className={`flex cursor-pointer items-center gap-2 px-3 py-1.5 text-[13px] select-none ${
                i === activeIndex ? "bg-accent/15 text-fg" : "text-muted hover:bg-panel"
              }`}
              onMouseEnter={() => onHover(i)}
              onMouseDown={(ev) => { ev.preventDefault(); onSelect(e.name, e.dir); }}
              onTouchEnd={(ev) => { ev.preventDefault(); onSelect(e.name, e.dir); }}
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
