/**
 * AtMention – @path file autocomplete for composer inputs.
 *
 * Usage:
 *   <AtMentionInput nodeId="..." cwd="/some/dir" ...textareaProps />
 *
 * When the user types @, a panel appears above the input listing files and
 * directories from the node. Arrow keys / Enter / click pick a path; Escape
 * dismisses. The chosen path replaces the @… token in the text.
 */
import { forwardRef, useCallback, useEffect, useRef, useState } from "react";
import { File, Folder } from "lucide-react";
import { api } from "../api";

// ── types ────────────────────────────────────────────────────────────────────

interface Entry {
  name: string;
  dir: boolean;
}

interface AtState {
  startIndex: number; // index of the '@' character in value
  query: string;      // text after '@', e.g. "src/comp"
}

// ── hook: detect @query and fetch completions ─────────────────────────────

function useAtMention(value: string, cursorPos: number, nodeId: string, cwd: string) {
  const [atState, setAtState] = useState<AtState | null>(null);
  const [entries, setEntries] = useState<Entry[]>([]);
  const [loading, setLoading] = useState(false);
  const fetchRef = useRef(0);

  // Recompute atState whenever value or cursor moves.
  useEffect(() => {
    const before = value.slice(0, cursorPos);
    // Find the last @ that isn't preceded by a word char (so @@ or email is ignored).
    const match = before.match(/(?:^|[\s\n])@([\S]*)$/);
    if (!match) {
      setAtState(null);
      return;
    }
    const query = match[1];
    const startIndex = before.lastIndexOf("@");
    setAtState({ startIndex, query });
  }, [value, cursorPos]);

  // Fetch directory listing when query changes.
  useEffect(() => {
    if (!atState) {
      setEntries([]);
      return;
    }
    const { query } = atState;
    // Split into dir + prefix.
    const slash = query.lastIndexOf("/");
    const dirPart = slash >= 0 ? query.slice(0, slash) : "";
    const prefix = slash >= 0 ? query.slice(slash + 1) : query;
    // Resolve dir relative to cwd.
    const dir = dirPart.startsWith("/") ? dirPart : [cwd, dirPart].filter(Boolean).join("/");

    const id = ++fetchRef.current;
    setLoading(true);
    api.files(nodeId, dir || cwd)
      .then((list) => {
        if (fetchRef.current !== id) return;
        const filtered = (list.entries ?? [])
          .filter((e) => e.name.toLowerCase().startsWith(prefix.toLowerCase()))
          .slice(0, 10)
          .map((e) => ({ name: e.name, dir: e.dir }));
        setEntries(filtered);
      })
      .catch(() => { if (fetchRef.current === id) setEntries([]); })
      .finally(() => { if (fetchRef.current === id) setLoading(false); });
  }, [atState?.query, nodeId, cwd]);

  return { atState, entries, loading };
}

// ── AtMentionPanel ────────────────────────────────────────────────────────

interface PanelProps {
  entries: Entry[];
  loading: boolean;
  activeIndex: number;
  onSelect: (name: string, isDir: boolean) => void;
  onHover: (i: number) => void;
}

function AtMentionPanel({ entries, loading, activeIndex, onSelect, onHover }: PanelProps) {
  if (!loading && entries.length === 0) return null;
  return (
    <div className="border-t border-line">
      {loading && entries.length === 0 ? (
        <div className="px-3 py-2 text-[12px] text-faint">加载中…</div>
      ) : (
        <ul role="listbox" className="max-h-48 overflow-y-auto py-1">
          {entries.map((e, i) => (
            <li
              key={e.name}
              role="option"
              aria-selected={i === activeIndex}
              className={`flex cursor-pointer items-center gap-2 px-3 py-1.5 text-[13px] ${i === activeIndex ? "bg-accent/15 text-fg" : "text-muted hover:bg-panel"}`}
              onMouseEnter={() => onHover(i)}
              onMouseDown={(ev) => { ev.preventDefault(); onSelect(e.name, e.dir); }}
            >
              {e.dir
                ? <Folder className="h-3.5 w-3.5 shrink-0 text-info" />
                : <File className="h-3.5 w-3.5 shrink-0 text-faint" />}
              <span className="truncate font-mono">{e.name}{e.dir ? "/" : ""}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// ── AtMentionInput ────────────────────────────────────────────────────────

export interface AtMentionInputProps extends React.TextareaHTMLAttributes<HTMLTextAreaElement> {
  nodeId: string;
  cwd: string;
  value: string;
  onValueChange: (v: string) => void;
  onSend?: () => void;
}

export const AtMentionInput = forwardRef<HTMLTextAreaElement, AtMentionInputProps>(
  function AtMentionInput({ nodeId, cwd, value, onValueChange, onSend, onKeyDown, ...rest }, forwardedRef) {
    const innerRef = useRef<HTMLTextAreaElement>(null);
    const ref = (forwardedRef as React.RefObject<HTMLTextAreaElement>) ?? innerRef;
    const [cursorPos, setCursorPos] = useState(0);
    const [activeIndex, setActiveIndex] = useState(0);
    const { atState, entries, loading } = useAtMention(value, cursorPos, nodeId, cwd);
    const open = !!atState && (loading || entries.length > 0);

    // Reset active index when list changes.
    useEffect(() => { setActiveIndex(0); }, [entries]);

    const accept = useCallback((name: string, isDir: boolean) => {
      if (!atState) return;
      const before = value.slice(0, atState.startIndex);
      const after = value.slice(cursorPos);
      // If directory, keep it open for further completion.
      const slash = atState.query.lastIndexOf("/");
      const dirPrefix = slash >= 0 ? atState.query.slice(0, slash + 1) : "";
      const inserted = isDir ? `@${dirPrefix}${name}/` : `@${dirPrefix}${name} `;
      const next = before + inserted + after;
      onValueChange(next);
      // Move cursor to end of inserted text.
      const newPos = before.length + inserted.length;
      requestAnimationFrame(() => {
        const el = ref && "current" in ref ? ref.current : null;
        el?.setSelectionRange(newPos, newPos);
        el?.focus();
        setCursorPos(newPos);
      });
    }, [atState, value, cursorPos, onValueChange, ref]);

    const handleKeyDown = useCallback((e: React.KeyboardEvent<HTMLTextAreaElement>) => {
      if (open) {
        if (e.key === "ArrowDown") {
          e.preventDefault();
          setActiveIndex((i) => Math.min(i + 1, entries.length - 1));
          return;
        }
        if (e.key === "ArrowUp") {
          e.preventDefault();
          setActiveIndex((i) => Math.max(i - 1, 0));
          return;
        }
        if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
          e.preventDefault();
          if (entries[activeIndex]) accept(entries[activeIndex].name, entries[activeIndex].dir);
          return;
        }
        if (e.key === "Escape") {
          e.preventDefault();
          onValueChange(value); // triggers re-render, atState drops naturally when cursor moves
          setCursorPos((p) => p); // force recompute
          return;
        }
        if (e.key === "Tab") {
          e.preventDefault();
          if (entries[activeIndex]) accept(entries[activeIndex].name, entries[activeIndex].dir);
          return;
        }
      }
      onKeyDown?.(e);
    }, [open, entries, activeIndex, accept, onKeyDown, value, onValueChange]);

    return (
      <>
        {open && (
          <AtMentionPanel
            entries={entries}
            loading={loading}
            activeIndex={activeIndex}
            onSelect={accept}
            onHover={setActiveIndex}
          />
        )}
        <textarea
          ref={ref}
          value={value}
          onChange={(e) => {
            onValueChange(e.target.value);
            setCursorPos(e.target.selectionStart ?? e.target.value.length);
          }}
          onSelect={(e) => setCursorPos(e.currentTarget.selectionStart ?? 0)}
          onKeyDown={handleKeyDown}
          {...rest}
        />
      </>
    );
  }
);
