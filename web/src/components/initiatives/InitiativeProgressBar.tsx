// C27 shared component (owned by C27T1's list work; C27T2 detail imports
// this module rather than forking it). T1's branch had not merged into
// main at implementation time, so this file implements the documented
// contract exactly:
//   props { completionPct: number, doneCount?: number, total?: number }
// rendering a labeled progress bar.
// T1's canonical version takes precedence on merge if it differs.
import { cn } from "cn";

interface InitiativeProgressBarProps {
  completionPct: number;
  doneCount?: number;
  total?: number;
  className?: string;
}

export default function InitiativeProgressBar({
  completionPct,
  doneCount,
  total,
  className,
}: InitiativeProgressBarProps) {
  const pct = Math.max(0, Math.min(100, Number(completionPct) || 0));
  const rounded = Math.round(pct);
  return (
    <div className={cn("w-full", className)}>
      <div className="mb-1 flex items-baseline justify-between gap-2">
        <span className="text-[11px] font-medium text-ink-muted">Progress</span>
        <span className="text-[11px] font-semibold text-ink tabular-nums">
          {rounded}%
          {doneCount != null && total != null && (
            <span className="ml-1.5 font-normal text-ink-muted">
              {doneCount} of {total} issues done
            </span>
          )}
        </span>
      </div>
      <div
        className="h-2 w-full overflow-hidden rounded-full bg-raised"
        role="progressbar"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={rounded}
        aria-label="Initiative progress"
      >
        <div
          className="h-full rounded-full bg-emerald-500 transition-[width]"
          style={{ width: `${pct}%` }}
        />
      </div>
    </div>
  );
}
