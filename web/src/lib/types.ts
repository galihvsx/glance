// Shared domain types mirroring the backend JSON shapes (snake_case).

import type { CustomValue } from "./customFields";

export interface Workspace {
  id: string;
  slug: string;
  name: string;
  /** 20 = admin, 15 = member, 5 = guest */
  role: number;
  /** Whether a Slack incoming-webhook URL is configured (C9T3). The URL itself is never returned. */
  slack_configured: boolean;
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

/** One workspace member with their user identity. */
export interface WorkspaceMember {
  id: string;
  name?: string | null;
  email: string;
  /** 20 = admin, 15 = member, 5 = guest */
  role: number;
}

/** Role values accepted by the members endpoints. */
export const WORKSPACE_ROLES = [
  { value: 20, label: "Admin" },
  { value: 15, label: "Member" },
  { value: 5, label: "Guest" },
] as const;

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
  /** Child summaries; present only on the detail endpoint fetched with
   *  ?include_children=1. */
  children?: IssueChild[];
  /** Custom values keyed by field_id; present only on the detail endpoint
   *  fetched with ?include_custom=1 (C7T3). */
  custom_values?: Record<string, CustomValue>;
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
  /** Subtask rollup (C17T2); omitted on endpoints that don't select it. */
  subtask_progress?: SubtaskProgress | null;
}

/** Subtask progress rollup (C17T2): DIRECT children only; `done` counts
 *  children in a 'completed'-group state. Present on detail and list
 *  responses; zero-subtask issues carry {total: 0, done: 0}. */
export interface SubtaskProgress {
  total: number;
  done: number;
}

/** One child summary attached to the issue detail response when fetched
 *  with ?include_children=1 (C5T4). `state` is the state's display name. */
export interface IssueChild {
  uuid: string;
  identifier: string;
  title: string;
  state: string;
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

/** One issue_subscribers row (GET .../issues/{uuid}/subscribers). */
export interface Subscriber {
  user_id: string;
  name?: string | null;
  email: string;
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

/** A project module (epic / sub-grouping). Dates are YYYY-MM-DD or null. */
export interface Module {
  id: string;
  project_id: string;
  name: string;
  description?: string | null;
  status: "active" | "completed" | "archived";
  lead_id?: string | null;
  start_date?: string | null;
  target_date?: string | null;
  issue_count: number;
  created_at: string;
  updated_at: string;
}

/** A project release/milestone (C4T6). release_date is YYYY-MM-DD or null. */
export interface Release {
  id: string;
  project_id: string;
  name: string;
  description: string;
  status: "planned" | "released";
  release_date?: string | null;
  issue_count: number;
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

/** A project wiki/documentation page. `parent_id` is null/absent for roots. */
export interface Page {
  id: string;
  project_id: string;
  parent_id?: string | null;
  title: string;
  content: string;
  position: number;
  author_id?: string | null;
  created_at: string;
  updated_at: string;
}

/** One content snapshot of a page (newest first from the API). */
export interface PageRevision {
  id: string;
  page_id: string;
  title: string;
  content: string;
  author_id?: string | null;
  created_at: string;
}

export interface TimeEntry {
  id: string;
  issue_id: string;
  user_id: string;
  started_at: string;
  ended_at?: string | null;
  note: string;
  created_at: string;
}

export interface Attachment {
  id: string;
  issue_id: string;
  filename: string;
  content_type: string;
  size_bytes: number;
  uploaded_by: {
    id: string;
    name?: string | null;
    email: string;
  };
  created_at: string;
}

/** A directed dependency edge between issues (C4T0). `issue_id` blocks
 *  `target_issue_id` when kind is "blocks". */
export interface IssueLink {
  id: string;
  issue_id: string;
  target_issue_id: string;
  kind: string;
  created_at: string;
  direction?: string;
}
