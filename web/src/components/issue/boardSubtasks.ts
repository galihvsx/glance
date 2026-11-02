import { PRIORITY_LABELS, type Issue, type SubtaskRow } from "../../lib/types";

/** Normalize a subtask row's priority to the 0-4 level PriorityBars
 *  expects, or null when the row carries none. Accepts the numeric
 *  wire form (the codebase's issue-priority serialization) and the
 *  plan-contract label string ("High") — see SubtaskRow in types.ts
 *  for why both are accepted pending the T0/T1 merge check. */
export function subtaskPriorityLevel(
  priority: SubtaskRow["priority"],
): number | null {
  if (typeof priority === "number") return priority;
  if (typeof priority === "string") {
    const byLabel = (PRIORITY_LABELS as readonly string[]).indexOf(priority);
    if (byLabel >= 0) return byLabel;
    const asNumber = Number(priority);
    if (Number.isFinite(asNumber)) return asNumber;
  }
  return null;
}

export interface SplitBoardIssues {
  /** Cards to render: every fetched issue except a child nested
   *  under a visible (top-level) parent card. */
  visibleIssues: Issue[];
  /** Parent id → its nested rows, sorted by sort_order ASC. Only
   *  top-level parents appear here: a row never itself carries
   *  nested rows this cycle (direct children, one level). */
  nestedByParent: Map<string, SubtaskRow[]>;
}

/** C122T1 board split. Toggle off (or a fetch without the flag, where
 *  every `subtasks` is undefined) returns today's flat layout
 *  untouched — no grouping, no hiding.
 *
 *  Orphan rule (binding): a child whose parent is NOT in the fetched
 *  set never appears in any parent's `subtasks`, so it stays a flat
 *  card in its own state column. A child NEVER vanishes — and the
 *  same holds for a grandchild: its parent is itself a nested row,
 *  not a card, so the grandchild's row is not collected here and its
 *  flat card stays. */
export function splitBoardIssues(
  issues: Issue[],
  showSubWorkItems: boolean,
): SplitBoardIssues {
  if (!showSubWorkItems) {
    return { visibleIssues: issues, nestedByParent: new Map() };
  }
  // Every id that appears as a direct child of any fetched issue.
  const childIds = new Set<string>();
  for (const issue of issues) {
    for (const row of issue.subtasks ?? []) childIds.add(row.id);
  }
  // Top-level = fetched issues that are nobody's child. Only their
  // subtask rows nest under a visible card.
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
