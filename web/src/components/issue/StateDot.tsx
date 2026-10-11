import type { IssueState } from "../../lib/types";
import { cn } from "cn";

/**
 * State dot per the cycle-19 spec (Screen 2): 8x8px circle, 1.5px solid
 * currentColor. The ring style follows the state group — hollow for
 * triage/backlog/unstarted, dashed for started, filled for completed,
 * and a diagonal slash for cancelled. The color comes from the state
 * itself so custom states keep their identity.
 */
export default function StateDot({
  state,
  className,
}: {
  state: IssueState;
  className?: string;
}) {
  const group = state.group;
  const filled = group === "completed";
  const dashed = group === "started";
  const slashed = group === "cancelled";
  return (
    <span
      className={cn("relative inline-block h-2 w-2 shrink-0 rounded-full", className)}
      style={{
        border: `1.5px solid ${state.color}`,
        borderStyle: dashed ? "dashed" : "solid",
        backgroundColor: filled ? state.color : "transparent",
        color: state.color,
      }}
      aria-hidden
    >
      {slashed && (
        <span
          className="absolute top-1/2 left-1/2 h-[7px] w-[1px] -translate-x-1/2 -translate-y-1/2 -rotate-45"
          style={{ backgroundColor: state.color }}
        />
      )}
    </span>
  );
}
