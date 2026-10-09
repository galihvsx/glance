import { useState } from "react";

/** Multi-select state for the issues list view (C5T8 bulk operations).
 *  Selection is a set of issue ids, independent of the rendered rows —
 *  it survives pagination and re-renders. "Select all" unions every
 *  loaded page. The pure helpers below are vitest-covered; the hook is
 *  a thin state wrapper.
 */

/** Toggle one id in the selection. Pure — returns a new Set. */
export function toggleSelected(
  sel: ReadonlySet<string>,
  id: string,
): Set<string> {
  const next = new Set(sel);
  if (next.has(id)) {
    next.delete(id);
  } else {
    next.add(id);
  }
  return next;
}

/** Union every id into the selection (insertion order kept, dupes
 *  ignored). Pure — returns a new Set. */
export function selectAllIds(
  sel: ReadonlySet<string>,
  ids: string[],
): Set<string> {
  const next = new Set(sel);
  for (const id of ids) {
    next.add(id);
  }
  return next;
}

/** The bulk `set` payload sent to PATCH /issues/bulk. Every field is
 *  optional: absent = untouched; label_ids [] = clear labels;
 *  assignee_id null = clear assignees. */
export interface BulkSetPayload {
  state_id?: string;
  priority?: number;
  label_ids?: string[];
  assignee_id?: string | null;
}

/** Draft edits staged in the bulk action bar. stateId/priority/labelIds
 *  use null = untouched (labelIds [] = clear); assigneeId uses undefined
 *  = untouched, null = clear, string = set to that user. */
export interface BulkSetDraft {
  stateId: string | null;
  priority: number | null;
  labelIds: string[] | null;
  assigneeId: string | null | undefined;
}

/** Build the API payload from the bar draft. Returns null when the user
 *  touched nothing (Apply stays disabled). */
export function buildBulkSetPayload(
  draft: BulkSetDraft,
): BulkSetPayload | null {
  const set: BulkSetPayload = {};
  let touched = false;
  if (draft.stateId !== null) {
    set.state_id = draft.stateId;
    touched = true;
  }
  if (draft.priority !== null) {
    set.priority = draft.priority;
    touched = true;
  }
  if (draft.labelIds !== null) {
    set.label_ids = [...draft.labelIds];
    touched = true;
  }
  if (draft.assigneeId !== undefined) {
    set.assignee_id = draft.assigneeId;
    touched = true;
  }
  return touched ? set : null;
}

const EMPTY: ReadonlySet<string> = new Set();

/** List-view multi-select state. */
export function useBulkSelection() {
  const [selected, setSelected] = useState<ReadonlySet<string>>(EMPTY);
  return {
    selected,
    selectedIds: [...selected],
    count: selected.size,
    isSelected: (id: string) => selected.has(id),
    toggle: (id: string) => setSelected((s) => toggleSelected(s, id)),
    selectAll: (ids: string[]) => setSelected((s) => selectAllIds(s, ids)),
    clear: () => setSelected(EMPTY),
  };
}
