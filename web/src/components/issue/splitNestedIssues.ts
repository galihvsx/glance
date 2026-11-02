// C124T0: shared split for the board AND the list view — extracted
// verbatim from C122T1's splitBoardIssues (boardSubtasks.ts) so both
// views nest with exactly the same semantics. T1 imports ONLY from
// here, never duplicates the split logic.
import type { Issue, SubtaskRow } from "../../lib/types";

export interface SplitNestedIssues {
  /** Rows to render: every fetched issue except a child nested
   *  under a visible (top-level) parent row/card. */
  visibleIssues: Issue[];
  /** Parent id → its nested rows, sorted by sort_order ASC. Only
   *  top-level parents appear here: a row never itself carries
   *  nested rows this cycle (direct children, one level). Computed
   *  from the FULL fetched set, before any group filtering, so a
   *  parent appearing in several groups (epic multi-appearance)
   *  shows its nested rows under EACH appearance. */
  nestedByParent: Map<string, SubtaskRow[]>;
}

/** Toggle off (or a fetch without the flag, where every `subtasks`
 *  is undefined) returns today's flat layout untouched — no
 *  grouping, no hiding.
 *
 *  Orphan rule (binding): a child whose parent is NOT in the fetched
 *  set never appears in any parent's `subtasks`, so it stays a flat
 *  row in its own group. A child NEVER vanishes — and the same holds
 *  for a grandchild: its parent is itself a nested row, not a
 *  top-level row, so the grandchild's row is not collected here and
 *  its flat row stays. */
export function splitNestedIssues(
  issues: Issue[],
  showSubWorkItems: boolean,
): SplitNestedIssues {
  if (!showSubWorkItems) {
    return { visibleIssues: issues, nestedByParent: new Map() };
  }
  // Every id that appears as a direct child of any fetched issue.
  const childIds = new Set<string>();
  for (const issue of issues) {
    for (const row of issue.subtasks ?? []) childIds.add(row.id);
  }
  // Top-level = fetched issues that are nobody's child. Only their
  // subtask rows nest under a visible row/card.
  const nestedByParent = new Map<string, SubtaskRow[]>();
  const nestedIds = new Set<string>();
  for (const issue of issues) {
    if (childIds.has(issue.id)) continue;
    const rows = issue.subtasks ?? [];
    if (rows.length === 0) continue;
    nestedByParent.set(
      issue.id,
      [...rows].sort((a, b) => a.sort_order - b.sort_order),
    );
    for (const row of rows) nestedIds.add(row.id);
  }
  return {
    visibleIssues: issues.filter((i) => !nestedIds.has(i.id)),
    nestedByParent,
  };
}
