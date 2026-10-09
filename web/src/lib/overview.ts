// Project overview aggregation (C8T5): pure helpers over the
// GET .../projects/{identifier}/overview payload. Kept separate from
// the page so the stat math is unit-testable.

import type { ActivityEntry } from "./activity";

/** State row as shipped inside the overview payload. */
export interface OverviewState {
  id: string;
  name: string;
  group: string;
  color: string;
}

/** Cycle progress as shipped inside the overview payload. */
export interface OverviewCycle {
  id: string;
  name: string;
  status: string;
  completed: number;
  total: number;
}

/** The overview endpoint's response shape. */
export interface ProjectOverview {
  project: { id: string; identifier: string; name: string; description: string };
  role: number;
  summary: {
    by_state: Record<string, number>;
    by_priority: Record<string, number>;
    by_label: Record<string, number>;
    overdue_count: number;
    estimate_total: number;
    estimate_done: number;
  };
  states: OverviewState[];
  activity: ActivityEntry[];
  cycle: OverviewCycle | null;
  /** Open-issue counts by priority bucket ("0".."4"), server-computed. */
  open_by_priority: Record<string, number>;
}

/** State groups that count as "done" for completion math (mirrors the
 *  backend's doneStateGroups). */
const DONE_GROUPS = new Set(["completed", "cancelled"]);

/** Map state id → group from the overview's states list. */
export function stateGroupById(states: OverviewState[]): Map<string, string> {
  const m = new Map<string, string>();
  for (const s of states) m.set(s.id, s.group);
  return m;
}

export interface CompletionStats {
  total: number;
  done: number;
  open: number;
  /** Percent of live issues in a done group, 0-100. */
  percent: number;
}

/** Completion over the analytics by_state counts. Unknown state ids
 *  count as open (never silently dropped). */
export function completionStats(
  byState: Record<string, number>,
  states: OverviewState[],
): CompletionStats {
  const groups = stateGroupById(states);
  let total = 0;
  let done = 0;
  for (const [stateId, n] of Object.entries(byState)) {
    total += n;
    if (DONE_GROUPS.has(groups.get(stateId) ?? "")) done += n;
  }
  const open = total - done;
  return { total, done, open, percent: total === 0 ? 0 : Math.round((done / total) * 100) };
}

export interface PriorityBreakdown {
  priority: number;
  label: string;
  count: number;
}

/** Open-issue counts per priority bucket (index into PRIORITY_LABELS:
 *  0 None … 4 Urgent), from the endpoint's server-computed
 *  open_by_priority split. Missing buckets are zero. */
export function priorityBreakdown(
  openByPriority: Record<string, number>,
  labels: readonly string[],
): PriorityBreakdown[] {
  const out: PriorityBreakdown[] = [];
  for (let p = 0; p < labels.length; p++) {
    const count = openByPriority[String(p)] ?? 0;
    out.push({ priority: p, label: labels[p] ?? `P${p}`, count });
  }
  return out;
}

export interface Contributor {
  actor: string;
  changes: number;
}

/** Top contributors from the activity window: ranked by audit-entry
 *  count, ties broken alphabetically for determinism. */
export function topContributors(entries: ActivityEntry[], limit: number): Contributor[] {
  const counts = new Map<string, number>();
  for (const e of entries) counts.set(e.actor, (counts.get(e.actor) ?? 0) + 1);
  return [...counts.entries()]
    .map(([actor, changes]) => ({ actor, changes }))
    .sort((a, b) => b.changes - a.changes || a.actor.localeCompare(b.actor))
    .slice(0, Math.max(0, limit));
}

/** Cycle progress percent from the overview's cycle card. */
export function cyclePercent(cycle: OverviewCycle | null): number {
  if (!cycle || cycle.total === 0) return 0;
  return Math.round((cycle.completed / cycle.total) * 100);
}

/** The newest `limit` activity entries for inline display. The payload
 *  is already newest-first; this just slices. */
export function recentActivity(entries: ActivityEntry[], limit: number): ActivityEntry[] {
  return entries.slice(0, Math.max(0, limit));
}
