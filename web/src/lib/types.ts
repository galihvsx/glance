// Shared domain types mirroring the backend JSON shapes (snake_case).

export interface Workspace {
  id: string;
  slug: string;
  name: string;
  /** 20 = admin, 15 = member, 5 = guest */
  role: number;
  created_at: string;
  updated_at: string;
}

export interface Project {
  id: string;
  workspace_id: string;
  identifier: string;
  name: string;
  description: string;
  created_at: string;
  updated_at: string;
}

export interface IssueState {
  id: string;
  project_id: string;
  name: string;
  group: string;
  color: string;
  sequence: number;
  created_at: string;
  updated_at: string;
}

/** Human label for a workspace membership role. */
export function roleLabel(role: number): string {
  switch (role) {
    case 20:
      return "Admin";
    case 15:
      return "Member";
    case 5:
      return "Guest";
    default:
      return `Role ${role}`;
  }
}

// ---------- Issues (Tasks 14–18) ----------

/** One assignee aggregated onto an issue row. */
export interface IssueAssignee {
  id: string;
  name: string;
}

/** One label aggregated onto an issue row. */
export interface IssueLabel {
  id: string;
  name: string;
  color: string;
}

/** An issue row from the list/detail endpoints. `description` is a TipTap
 *  JSON doc, omitted from list responses unless ?fields=description. */
export interface Issue {
  id: string;
  project_id: string;
  sequence_id: number;
  display_id: string;
  name: string;
  description?: unknown;
  priority: number;
  state_id: string;
  parent_id?: string | null;
  sort_order: number;
  start_date?: string | null;
  target_date?: string | null;
  estimate_point_id?: string | null;
  is_draft: boolean;
  archived_at?: string | null;
  created_by: string;
  created_at: string;
  updated_at: string;
  assignees: IssueAssignee[];
  labels: IssueLabel[];
}

/** List envelope: {"results": [...], "next_cursor"?: "..."}. */
export interface IssueListResult {
  results: Issue[];
  next_cursor?: string;
}

/** A workspace member (for the assignee picker). */
export interface Member {
  id: string;
  name?: string | null;
  email: string;
  role: number;
}

/** A project label. */
export interface Label {
  id: string;
  name: string;
  color: string;
  parent_id?: string | null;
  created_at: string;
  updated_at: string;
}

export interface ActorRef {
  id: string;
  name?: string | null;
  email: string;
}

/** One comment node with nested replies. */
export interface CommentNode {
  id: string;
  issue_id: string;
  parent_id?: string | null;
  actor: ActorRef;
  content: unknown;
  created_at: string;
  updated_at: string;
  replies: CommentNode[];
}

/** One issue_activities row. */
export interface HistoryEntry {
  id: string;
  field: string;
  old_value?: unknown;
  new_value?: unknown;
  actor: ActorRef;
  created_at: string;
}

/** Priority labels (0–4). */
export const PRIORITY_LABELS = ["None", "Low", "Medium", "High", "Urgent"] as const;
export function priorityLabel(p: number): string {
  return PRIORITY_LABELS[p] ?? `P${p}`;
}

/** Intake status codes (mirror of the Go SMALLINT vocabulary). */
export const INTAKE_STATUS = {
  pending: 0,
  rejected: 1,
  snoozed: 2,
  accepted: 3,
  duplicate: 4,
} as const;

/**
 * One inbox item: an intake_issues row with the full issue JSON embedded
 * under `issue`. Note the intake query does not populate
 * assignees/labels on the embedded issue — they arrive as null, not [].
 */
export interface IntakeItem {
  id: string;
  issue_id: string;
  issue?: {
    id: string;
    display_id: string;
    name: string;
    created_at: string;
  } | null;
  status: number;
  status_name: string;
  snoozed_till?: string | null;
  duplicate_to_id?: string | null;
  created_at: string;
}

/** GET .../intake envelope. */
export interface IntakeInbox {
  intake: {
    id: string;
    project_id: string;
    name: string;
    is_default: boolean;
  };
  items: IntakeItem[];
  /** Still-snoozed items (snoozed_till in the future), ordered by wake-up. */
  snoozed: IntakeItem[];
}

// ---------- Cycles (Task 22–23) ----------

/** A cycle: start_date/end_date arrive as RFC3339 strings; the backend
 *  parses YYYY-MM-DD on write. progress_snapshot counts live issues by
 *  state group: triage, backlog, unstarted, started, completed, cancelled. */
export interface Cycle {
  id: string;
  project_id: string;
  name: string;
  start_date: string;
  end_date: string;
  status: "upcoming" | "current" | "completed";
  progress_snapshot: Record<string, number>;
  created_at: string;
  updated_at: string;
}

/** One day of a cycle burndown. `remaining` is null for future days. */
export interface BurndownDay {
  date: string; // YYYY-MM-DD
  remaining: number | null;
  ideal: number;
}

/** GET .../cycles/{id}/burndown response. */
export interface CycleBurndown {
  cycle_id: string;
  start_date: string; // YYYY-MM-DD
  end_date: string; // YYYY-MM-DD
  status: string;
  total_scope: number;
  days: BurndownDay[];
}
