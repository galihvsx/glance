import { Plus } from "lucide-react";

/**
 * Day-cell quick-add affordance (C26T3). A subtle "+" in the cell corner
 * that opens the create-issue dialog with the cell's day prefilled as the
 * target (due) date.
 *
 * It lives in the cell chrome — absolutely positioned in the corner, a
 * sibling of the chips, never a descendant of one — so it cannot
 * interfere with dnd-kit drags: the drag sensors only listen on the
 * draggable chips' own nodes, and this button carries no draggable
 * wiring. Hidden until the cell is hovered or the button is
 * keyboard-focused; the parent cell must carry `relative group`.
 */
export function DayQuickAddButton({
  dayKey,
  onQuickAdd,
}: {
  dayKey: string;
  onQuickAdd: (dayKey: string) => void;
}) {
  return (
    <button
      type="button"
      onClick={(e) => {
        // The cell has no click handler today; stop propagation so a
        // future one (or dnd-kit's post-drag click suppression) can't
        // swallow the quick-add.
        e.stopPropagation();
        onQuickAdd(dayKey);
      }}
      className="absolute right-1 top-1 inline-flex h-6 w-6 items-center justify-center rounded-[4px] text-ink-muted opacity-0 transition-opacity hover:bg-wash-hover hover:text-ink focus-visible:opacity-100 group-hover:opacity-100 group-focus-within:opacity-100"
      title={`Add issue on ${dayKey}`}
      aria-label={`Add issue on ${dayKey}`}
    >
      <Plus className="h-[15px] w-[15px]" aria-hidden />
    </button>
  );
}
