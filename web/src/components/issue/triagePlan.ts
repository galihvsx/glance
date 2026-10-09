// Maps an AI triage result onto the project's real taxonomy (C5T3).
// The backend already pins the model to real state/label names; this
// planner resolves those names to ids for one-click apply, skipping
// labels already on the issue and surfacing names that no longer match.

import type { AITriageResult } from "../../lib/ai";
import type {
  Issue,
  IssueState,
  Label as ProjectLabel,
} from "../../lib/types";

export interface TriageApplyPlan {
  /** Suggested priority 0-4 (backend clamps; shown verbatim). */
  priority: number;
  /** Project label ids to add (already-applied labels excluded). */
  labelIdsToAdd: string[];
  /** Suggested label names with no matching project label — shown greyed. */
  unmatchedLabelNames: string[];
  /** Project state id matching the suggested state name. */
  stateId?: string;
  /** Suggested state name with no matching state — shown greyed. */
  unmatchedStateName?: string;
}

type TaxonomyIssue = Pick<Issue, "priority" | "state_id" | "labels">;

/** Resolve a triage result against the project's states and labels. */
export function planTriageApply(
  result: AITriageResult,
  issue: TaxonomyIssue,
  states: IssueState[],
  labels: ProjectLabel[],
): TriageApplyPlan {
  const appliedLabelIds = new Set(issue.labels.map((l) => l.id));
  const byLabelName = new Map(
    labels.map((l) => [l.name.toLowerCase(), l]),
  );
  const labelIdsToAdd: string[] = [];
  const unmatchedLabelNames: string[] = [];
  for (const name of result.label_names ?? []) {
    const match = byLabelName.get(name.toLowerCase());
    if (!match) {
      unmatchedLabelNames.push(name);
      continue;
    }
    if (
      !appliedLabelIds.has(match.id) &&
      !labelIdsToAdd.includes(match.id)
    ) {
      labelIdsToAdd.push(match.id);
    }
  }

  let stateId: string | undefined;
  let unmatchedStateName: string | undefined;
  if (result.state_name) {
    const match = states.find(
      (s) => s.name.toLowerCase() === result.state_name!.toLowerCase(),
    );
    if (match) stateId = match.id;
    else unmatchedStateName = result.state_name;
  }

  return {
    priority: result.priority,
    labelIdsToAdd,
    unmatchedLabelNames,
    stateId,
    unmatchedStateName,
  };
}

/** Whether the plan has anything to apply (label or state changes). */
export function planHasActions(plan: TriageApplyPlan): boolean {
  return plan.labelIdsToAdd.length > 0 || plan.stateId !== undefined;
}
