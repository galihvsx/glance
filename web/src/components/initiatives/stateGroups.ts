// C71T1: shared state-group vocabulary for the initiative + epic progress
// UIs. Extracted from InitiativeDetail.tsx (C27T2) — the colors match the
// board state dots. "Done" always means completed + cancelled (see
// InitiativeProgressBar); epics reuse that rule.

/** State-group colors (matches board dots). */
export const GROUP_COLORS: Record<string, string> = {
  triage: "#8b7cf6",
  backlog: "#9aa0ae",
  unstarted: "#e5a13c",
  started: "#4f8ef7",
  completed: "#42c88a",
  cancelled: "#e5484d",
};

export const GROUP_ORDER = [
  "triage",
  "backlog",
  "unstarted",
  "started",
  "completed",
  "cancelled",
];

/** Epics track five groups (C71): triage issues never live inside an epic. */
export const EPIC_GROUP_ORDER = [
  "backlog",
  "unstarted",
  "started",
  "completed",
  "cancelled",
];

export function groupLabel(group: string): string {
  return group.charAt(0).toUpperCase() + group.slice(1);
}

/** "Done" count for the progress captions: completed + cancelled. */
export function doneCount(byGroup: Record<string, number>): number {
  return (byGroup.completed ?? 0) + (byGroup.cancelled ?? 0);
}
