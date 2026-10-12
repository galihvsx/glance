// InitiativeProgressBar (C27T1): the shared initiative progress bar.
//
// Owned by C27T1; imported by the initiatives list (C27T1) and the
// initiative detail page (C27T2). Renders the rollup's completion_pct
// as a thin filled track with a right-aligned pct label, and — when
// the caller passes issue counts — a "done of total" caption.
//
// Style: 6px track, accent-blue fill (matches PageHeader's primary
// action), tabular-nums label; done = done-group issues
// (completed/cancelled) as counted by the backend rollup.

import { cn } from "../../lib/utils";

export interface InitiativeProgressBarProps {
  /** Completion percentage 0–100, from the rollup's `completion_pct`.
   *  Values outside 0–100 are clamped. */
  completionPct: number;
  /** Live (non-deleted) issues across member projects. When omitted,
   *  only the percentage label is shown. */
  total?: number;
  /** Issues in a done state group (completed/cancelled). */
  doneCount?: number;
  /** Extra class names for the wrapper. */
  className?: string;
}

function clampPct(pct: number): number {
  if (!Number.isFinite(pct)) return 0;
  return Math.min(100, Math.max(0, pct));
}

export default function InitiativeProgressBar({
  completionPct,
  total,
  doneCount,
  className,
}: InitiativeProgressBarProps) {
  const pct = clampPct(completionPct);
  const label = `${Math.round(pct)}%`;
  const showCounts = total != null && doneCount != null;
  return (
    <div className={cn("w-full", className)}>
      <div className="flex items-center gap-2">
        <div
          role="progressbar"
          aria-label={`Initiative progress ${label}`}
          aria-valuenow={Math.round(pct)}
          aria-valuemin={0}
          aria-valuemax={100}
          className="h-[6px] flex-1 overflow-hidden rounded-full bg-wash-active"
        >
          <div
            className="h-full rounded-full bg-accent-blue transition-[width]"
            style={{ width: `${pct}%` }}
          />
        </div>
        <span className="shrink-0 text-[11px] font-semibold tabular-nums text-ink">
          {label}
        </span>
      </div>
      {showCounts && (
        <p className="mt-1 text-[10px] tabular-nums text-ink-muted">
          {doneCount} of {total} issues done
        </p>
      )}
    </div>
  );
}
