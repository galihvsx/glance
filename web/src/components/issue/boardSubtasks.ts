// C124T0: the split itself moved to splitNestedIssues.ts (shared
// board + list helper, same semantics); this file keeps only
// subtaskPriorityLevel. Board imports splitNestedIssues directly.
import { PRIORITY_LABELS, type SubtaskRow } from "../../lib/types";

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
