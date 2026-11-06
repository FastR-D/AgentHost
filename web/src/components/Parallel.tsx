import { useState } from "react";
import clsx from "clsx";
import { Users } from "lucide-react";
import { Sheet } from "./DirPicker";

const CHOICES = [1, 2, 3, 4, 5, 6, 8];

export const parallelLabel = (n?: number) => (n && n > 1 ? `${n} 个 Codex 并行` : "1 个 Codex");

/** How many Codex work on a task at once. */
export function ParallelChip({ value, onChange }: { value?: number; onChange: (n: number) => void }) {
  const [open, setOpen] = useState(false);
  const n = value && value > 1 ? value : 1;
  return (
    <>
      <button
        type="button"
        className={clsx("chip shrink-0 cursor-pointer !py-1 hover:text-fg", n > 1 && "!border-info/40 !bg-info/10 !text-info")}
        onClick={() => setOpen(true)}
        title="同时用几个 Codex 做这个任务"
      >
        <Users className="h-3 w-3 shrink-0" /> {parallelLabel(n)}
      </button>
      {open && (
        <Sheet
          title={
            <span className="flex items-center gap-2">
              <Users className="h-4 w-4 text-info" /> 同时用几个 Codex
            </span>
          }
          onClose={() => setOpen(false)}
        >
          <div className="space-y-3 p-4">
            <div className="grid grid-cols-4 gap-2">
              {CHOICES.map((c) => (
                <button
                  key={c}
                  onClick={() => {
                    onChange(c);
                    setOpen(false);
                  }}
                  className={clsx(
                    "rounded-xl border py-3 text-center text-[15px] font-medium transition",
                    c === n ? "border-info bg-info/10 text-info" : "border-line hover:bg-sunken",
                  )}
                >
                  {c}
                </button>
              ))}
            </div>
            <p className="text-[12.5px] leading-relaxed text-muted">
              选 2 个以上时，Codex 会先把任务拆成互不冲突的几部分，交给几个子 Codex 同时做，自己负责分派、检查和汇总。适合能拆开的大任务，比如同时改几个模块、并行跑几组测试。
            </p>
            <p className="text-[12px] leading-relaxed text-faint">
              它们在同一个目录里干活。token 消耗大约随数量成倍增加；没法拆开的任务，Codex 会自己做。
            </p>
          </div>
        </Sheet>
      )}
    </>
  );
}
