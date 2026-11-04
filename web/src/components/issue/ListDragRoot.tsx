import { useState, type ReactNode } from "react";
import {
  DndContext,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  DragOverlay,
} from "@dnd-kit/core";
import { sortableKeyboardCoordinates } from "@dnd-kit/sortable";
import type { Issue, IssueState, SubtaskRow } from "../../lib/types";

/**
 * C127T0: the list view's single dnd-kit context. C126 owned one
 * context per stack, which made cross-group drops impossible — a drag
 * started in one context cannot land on another context's droppable —
 * and the C127 feature needs a row from ANY stack to land on ANY
 * group's header. So exactly one context owns the whole list (headers
 * + all stacks, no nesting). Per-stack isolation is preserved by
 * dispatch, not by context: ListRowStack's SortableContext items are
 * stack-scoped ids, and the page routes cross-stack row drops to a
 * silent no-op. This satisfies the plan's invariant (exactly one
 * context owns each header/stack pair, no contexts nest) with the one
 * shape that actually supports cross-group header drops.
 *
 * When `enabled` is false this renders only its children (no DOM, no
 * context), so drag-disabled lists are byte-identical to today. The
 * overlay renders the dragged IssueRow face — which also means the
 * mouseup click after a drag lands on the overlay, not the row, so a
 * drag never opens the peek drawer (C126 precedent). C141T1: a nested
 * active id renders the NestedListSubtaskRow face instead, via the
 * optional nested overlay props. Sensors are the
 * C126 pair: 6px pointer activation (a plain click is never a drag)
 * + keyboard dragging via sortableKeyboardCoordinates (keyboard drag
 * to a HEADER is out of scope this cycle — see the plan — keyboard
 * users keep the row-menu/peek paths, and C126's keyboard within-stack
 * reorder is untouched).
 */
export default function ListDragRoot({
  enabled,
  onDragStart,
  onDragEnd,
  overlayIssueForId,
  renderOverlayRow,
  overlayNestedForId,
  renderOverlayNestedRow,
  children,
}: {
  enabled: boolean;
  onDragStart?: () => void;
  /** (active sortable id, over id or null). */
  onDragEnd: (activeId: string, overId: string | null) => void;
  overlayIssueForId: (sortableId: string) => Issue | undefined;
  /** Plain (non-sortable) IssueRow face for the drag overlay. */
  renderOverlayRow: (issue: Issue) => ReactNode;
  /**
   * C141T1: nested-face resolution for a nested active id — the
   * dragged nested row plus its state, or undefined when the active
   * id is not a registered nested id. Absent, a nested drag shows
   * no overlay (the pre-C141 shape, kept only for non-page users).
   */
  overlayNestedForId?: (sortableId: string) => {
    row: SubtaskRow;
    state: IssueState | undefined;
  } | undefined;
  /** Plain (non-sortable) NestedListSubtaskRow face for the overlay. */
  renderOverlayNestedRow?: (
    row: SubtaskRow,
    state: IssueState | undefined,
  ) => ReactNode;
  children: ReactNode;
}) {
  const [activeId, setActiveId] = useState<string | null>(null);
  const pointerSensor = useSensor(PointerSensor, {
    activationConstraint: { distance: 6 },
  });
  const keyboardSensor = useSensor(KeyboardSensor, {
    coordinateGetter: sortableKeyboardCoordinates,
  });
  const sensors = useSensors(pointerSensor, keyboardSensor);

  if (!enabled) return <>{children}</>;

  const activeIssue = activeId ? overlayIssueForId(activeId) : undefined;
  const activeNested =
    !activeIssue && activeId ? overlayNestedForId?.(activeId) : undefined;
  return (
    <DndContext
      sensors={sensors}
      onDragStart={(e) => {
        setActiveId(String(e.active.id));
        onDragStart?.();
      }}
      onDragEnd={(e) => {
        setActiveId(null);
        onDragEnd(String(e.active.id), e.over ? String(e.over.id) : null);
      }}
      onDragCancel={() => setActiveId(null)}
    >
      {children}
      <DragOverlay>
        {activeIssue
          ? renderOverlayRow(activeIssue)
          : activeNested && renderOverlayNestedRow
            ? renderOverlayNestedRow(activeNested.row, activeNested.state)
            : null}
      </DragOverlay>
    </DndContext>
  );
}
