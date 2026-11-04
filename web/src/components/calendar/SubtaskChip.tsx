import { stateDotClass } from "../../lib/stateDot";
import type { IssueState, SubtaskRow } from "../../lib/types";
import { cn } from "../../lib/utils";

/**
 * C145T2: one nested sub-work chip under a parent calendar chip, in
 * the SAME day cell (or unscheduled-strip entry) as the parent — the
 * child travels with its parent, never as a flat chip of its own.
 *
 * Display + click-to-peek only: never draggable (no nested drag on
 * the calendar this cycle). Indented under the parent chip; the
 * chip shows the child's own start/target date text from T1's
 * SubtaskRow fields. A dateless child shows no date text, never a
 * fabricated date.
 */

/** Roadmap's formatDay precedent: UTC-parsed YYYY-MM-DD, "Nov 5". */
function formatDay(isoDay: string): string {
  const [y, m, d] = isoDay.split("-").map(Number);
  return new Date(Date.UTC(y, m - 1, d)).toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    timeZone: "UTC",
  });
}

/** The child's own date text, or "" when the child has no dates. */
export function subtaskDateText(row: SubtaskRow): string {
  const s = row.start_date?.slice(0, 10);
  const t = row.target_date?.slice(0, 10);
  if (s && t) {
    return s === t ? formatDay(t) : `${formatDay(s)} → ${formatDay(t)}`;
  }
  if (t) return `Due ${formatDay(t)}`;
  if (s) return `Starts ${formatDay(s)}`;
  return "";
}

export function SubtaskChip({
  row,
  state,
  onOpen,
}: {
  row: SubtaskRow;
  state?: IssueState;
  onOpen: () => void;
}) {
  const dateText = subtaskDateText(row);
  return (
    <div className="pl-4" data-nested-subtask={row.id}>
      <button
        type="button"
        onClick={onOpen}
        title={`${row.display_id}: ${row.title}`}
        className="flex w-full items-center gap-1.5 rounded-[4px] px-1.5 py-[3px] text-left text-[11px] text-ink-secondary hover:bg-wash-hover hover:text-ink focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
      >
        <span
          className={cn("state-dot", stateDotClass(state?.group))}
          aria-hidden
        />
        <span className="shrink-0 font-mono text-[10px] text-ink-muted">
          {row.display_id}
        </span>
        <span className="truncate">{row.title}</span>
        {dateText && (
          <span className="shrink-0 text-[10px] text-ink-muted">
            {dateText}
          </span>
        )}
      </button>
    </div>
  );
}
