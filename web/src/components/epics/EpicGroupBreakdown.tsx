// C71T1: stacked state-group progress for epics. Five segments (no
// triage — epics track backlog/unstarted/started/completed/cancelled),
// colors reused from GROUP_COLORS (shared with the initiative UI).
// The legend lists all five groups in fixed order (the C71 spec), with
// the done caption (completed + cancelled) matching InitiativeProgressBar.

import {
  EPIC_GROUP_ORDER,
  GROUP_COLORS,
  doneCount,
  groupLabel,
} from "../initiatives/stateGroups";

export default function EpicGroupBreakdown({
  byGroup,
  total,
  epicName,
}: {
  byGroup: Record<string, number>;
  total: number;
  epicName: string;
}) {
  if (total === 0) {
    return (
      <p className="text-[11px] text-ink-muted">
        No issues yet — attach issues from the epic detail page.
      </p>
    );
  }
  return (
    <div>
      <div
        className="flex h-[6px] w-full overflow-hidden rounded-full bg-raised"
        role="progressbar"
        aria-label={`${epicName} progress`}
        aria-valuemin={0}
        aria-valuemax={total}
        aria-valuenow={doneCount(byGroup)}
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
      <div className="mt-1.5 flex flex-wrap gap-x-3 gap-y-1">
        {EPIC_GROUP_ORDER.map((g) => (
          <span
            key={g}
            className="inline-flex items-center gap-1 text-[11px] text-ink-secondary tabular-nums"
          >
            <span
              className="size-2 rounded-full"
              style={{ backgroundColor: GROUP_COLORS[g] ?? "#777780" }}
              aria-hidden
            />
            {groupLabel(g)}
            <span className="text-ink-muted">{byGroup[g] ?? 0}</span>
          </span>
        ))}
      </div>
    </div>
  );
}
