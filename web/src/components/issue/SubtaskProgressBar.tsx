import type { SubtaskProgress } from "../../lib/types";
import { Progress } from "../ui/progress";

/** Subtask progress indicator (C17T2). Renders nothing when there are no
 *  subtasks, so zero-subtask issues render unchanged.
 *
 *  - `detail`: full bar plus "done/total" text, for the issue header.
 *  - `compact`: slim bar only, for list/board cards.
 */
export default function SubtaskProgressBar({
  progress,
  variant,
}: {
  progress: SubtaskProgress | null | undefined;
  variant: "detail" | "compact";
}) {
  if (!progress || progress.total <= 0) return null;
  const pct =
    progress.total > 0
      ? Math.round((progress.done / progress.total) * 100)
      : 0;
  if (variant === "compact") {
    return (
      <div
        className="h-1 w-16 overflow-hidden rounded-full bg-muted"
        role="progressbar"
        aria-valuenow={progress.done}
        aria-valuemin={0}
        aria-valuemax={progress.total}
        aria-label={`Subtasks: ${progress.done} of ${progress.total} done`}
        title={`${progress.done}/${progress.total} subtasks done`}
      >
        <div
          className="h-full rounded-full bg-primary transition-all"
          style={{ width: `${pct}%` }}
        />
      </div>
    );
  }
  return (
    <div
      className="flex items-center gap-2"
      title={`${progress.done} of ${progress.total} subtasks done`}
    >
      <Progress value={pct} className="w-40" />
      <span className="text-xs text-muted-foreground">
        {progress.done}/{progress.total}
      </span>
    </div>
  );
}
