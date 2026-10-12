import { useDroppable } from "@dnd-kit/core";
import type { ReactNode } from "react";
import { cn } from "../../lib/utils";
import { DayQuickAddButton } from "./DayQuickAddButton";

/**
 * A week-view day column that accepts dragged issue chips (C26T1). Same
 * drop semantics and highlight language as DroppableDayCell — the droppable
 * id is the day key, and `isOver` speaks the board column's drop language
 * (`border-accent-blue/70`) with a matching wash. Layout classes match the
 * week column: tall (`min-h-64`), no internal scroll, so the highlight
 * covers the full column.
 *
 * C26T3: `onQuickAdd` (omitted for guests) renders the "+" quick-add
 * affordance in the column corner — column chrome, not on the chips, so
 * chip drags are unaffected.
 */
export function DroppableWeekColumn({
  dayKey,
  isToday,
  onQuickAdd,
  children,
}: {
  dayKey: string;
  isToday: boolean;
  /** Omit to hide the quick-add affordance (guests, role < 15). */
  onQuickAdd?: (dayKey: string) => void;
  children: ReactNode;
}) {
  const { setNodeRef, isOver } = useDroppable({ id: dayKey });
  return (
    <div
      ref={setNodeRef}
      data-testid={`week-column-${dayKey}`}
      className={cn(
        "relative min-h-64 p-1 [&:not(:last-child)]:border-r [&:not(:last-child)]:border-r-border-hairline",
        onQuickAdd && "group",
        isToday && "bg-[rgba(79,142,247,0.05)]",
        isOver && "border-accent-blue/70 bg-[rgba(79,142,247,0.08)]",
      )}
    >
      {onQuickAdd && <DayQuickAddButton dayKey={dayKey} onQuickAdd={onQuickAdd} />}
      {children}
    </div>
  );
}
