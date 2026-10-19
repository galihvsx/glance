// C72T2: compact epic-lane header for the calendar swimlane gutter.
// A compact chrome variant of EpicGroupBreakdown (the full component's
// legend is too heavy for the 200px gutter) — but the progress contract
// is byte-identical: EPIC_GROUP_ORDER (5 segments, no triage),
// GROUP_COLORS, done = completed + cancelled.
//
// Lane groups always have >= 1 member (groupIssuesByEpic never produces
// empty groups; empty lanes are hidden), so total > 0 here.

import {
  EPIC_GROUP_ORDER,
  GROUP_COLORS,
  doneCount,
  groupLabel,
} from "../initiatives/stateGroups";
import { cn } from "../../lib/utils";

export function EpicLaneHeader({
  title,
  total,
  byGroup,
  isUngrouped,
}: {
  title: string;
  total: number;
  byGroup: Record<string, number>;
  isUngrouped: boolean;
}) {
  const done = doneCount(byGroup);
  return (
    <div className="px-2.5 py-2">
      <div className="flex items-baseline gap-1.5">
        <span
          className={cn(
            "truncate text-[12px]",
            isUngrouped
              ? "font-medium italic text-ink-secondary"
              : "font-semibold text-ink",
          )}
        >
          {title}
        </span>
        <span className="shrink-0 font-mono text-[10px] tabular-nums text-ink-muted">
          {total}
        </span>
      </div>
      <div
        className="mt-1.5 flex h-1 w-full max-w-[150px] overflow-hidden rounded-full bg-[rgba(255,255,255,0.06)]"
        role="progressbar"
        aria-label={`${title} progress`}
        aria-valuemin={0}
        aria-valuemax={total}
        aria-valuenow={done}
      >
        {EPIC_GROUP_ORDER.map((g) => {
          const n = byGroup[g] ?? 0;
          if (n === 0) return null;
          return (
            <div
              key={g}
              className="h-full"
              style={{
                width: `${(n / total) * 100}%`,
                backgroundColor: GROUP_COLORS[g] ?? "#777780",
              }}
              title={`${groupLabel(g)}: ${n}`}
            />
          );
        })}
      </div>
      <p className="mt-1 text-[10px] tabular-nums text-ink-muted">
        Done {done} of {total}
      </p>
    </div>
  );
}
