import { useDroppable } from "@dnd-kit/core";
import type { ReactNode } from "react";
import { cn } from "../../lib/utils";
import { DayQuickAddButton } from "./DayQuickAddButton";

/**
 * A month-grid day cell that accepts dragged issue chips (C26T0). Keeps the
 * cell's existing layout classes; the `isOver` highlight speaks the board
 * column's drop language (`border-accent-blue/70`) with a matching wash.
 *
 * C26T3: `onQuickAdd` (omitted for guests) renders the "+" quick-add
 * affordance in the cell corner — cell chrome, not on the chips, so chip
 * drags are unaffected.
 */
export function DroppableDayCell({
  dayKey,
  isOtherMonth,
  onQuickAdd,
  children,
  columns = 7,
}: {
  dayKey: string;
  isOtherMonth: boolean;
  /** Omit to hide the quick-add affordance (guests, role < 15). */
  onQuickAdd?: (dayKey: string) => void;
  children: ReactNode;
  /**
   * C72T2: grid column count for the row-end border rule. The grouped
   * month grid has a 200px swimlane gutter + 7 day columns (8), so the
   * last day of each row is `:nth-child(8n)` instead of `(7n)`.
   */
  columns?: 7 | 8;
}) {
  const { setNodeRef, isOver } = useDroppable({ id: dayKey });
  return (
    <div
      ref={setNodeRef}
      className={cn(
        "relative min-h-24 border-b border-border-hairline p-1",
        columns === 8
          ? "[&:not(:nth-child(8n))]:border-r [&:not(:nth-child(8n))]:border-r-border-hairline"
          : "[&:not(:nth-child(7n))]:border-r [&:not(:nth-child(7n))]:border-r-border-hairline",
        onQuickAdd && "group",
        isOtherMonth && "opacity-40",
        isOver && "border-accent-blue/70 bg-[rgba(79,142,247,0.08)]",
      )}
    >
      {onQuickAdd && <DayQuickAddButton dayKey={dayKey} onQuickAdd={onQuickAdd} />}
      {children}
    </div>
  );
}
