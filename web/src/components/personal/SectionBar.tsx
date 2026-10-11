// SectionBar + StatBand (C19T9): shared header/band language for the
// personal views, per the Cycle 19 prototype (SHELL CONTENT §3/§4).
//
// SectionBar: the "Recently updated" row language — 35px tall, bottom
// hairline, 12px/600 ink, muted count.
// StatBand: the prototype overview band — bordered 3-col grid, stat
// padding 15px, bg-surface, label 10px muted, value 18px/600, note
// 10px secondary.
import type { ReactNode } from "react";

export function SectionBar({
  label,
  count,
  action,
}: {
  label: string;
  count?: number | string;
  action?: ReactNode;
}) {
  return (
    <div className="flex h-[35px] items-center gap-2 border-b border-border-hairline text-[12px] font-semibold text-ink">
      <span>{label}</span>
      {count !== undefined && (
        <span className="font-normal text-ink-muted">{count}</span>
      )}
      {action && <span className="ml-auto flex items-center">{action}</span>}
    </div>
  );
}

export interface Stat {
  label: string;
  value: string | number;
  note?: string;
}

export function StatBand({ stats }: { stats: Stat[] }) {
  return (
    <div className="grid grid-cols-3 overflow-hidden rounded-lg border border-border">
      {stats.map((s, i) => (
        <div
          key={s.label}
          className={`bg-surface p-[15px] ${
            i < stats.length - 1 ? "border-r border-border" : ""
          }`}
        >
          <p className="mb-[7px] text-[10px] text-ink-muted">{s.label}</p>
          <p className="text-[18px] font-semibold leading-none text-ink">
            {s.value}
          </p>
          {s.note && (
            <p className="mt-[3px] text-[10px] text-ink-secondary">{s.note}</p>
          )}
        </div>
      ))}
    </div>
  );
}
