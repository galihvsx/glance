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
  /** Whether a GitHub repo-webhook secret is configured (C17T0, C62T0 UI). The secret itself is never returned. */
  github_webhook_configured: boolean;
  created_at: string;
  updated_at: string;
}

/** Workspace-scoped project group (C89T0): grouping + navigation only,
 *  no per-teamspace membership/ACL. project_count is the LIVE
 *  (non-archived) project count from the same list query. */
export interface Teamspace {
  id: string;
  workspace_id: string;
  name: string;
  project_count: number;
  created_at: string;
  updated_at: string;
}

export interface Project {
  id: string;
  workspace_id: string;
  identifier: string;
  name: string;
  description: string;
  /** Teamspace assignment (C89T0); null/undefined = ungrouped. */
  teamspace_id?: string | null;
  /** Project emoji icon (C18T4); null/undefined = unset. */
  icon?: string | null;
  /** Project archive timestamp (C18T3); null/undefined = live. */
  archived_at?: string | null;
  /** Per-project parallel cycles toggle (C78T0); off = single active
   *  cycle, no overlapping dates. Server always emits it. */
  parallel_cycles: boolean;
  /** Workspace-scoped project labels (C76T0): [{id,name,color}] ordered
   *  by name — [] never null. */
  labels: ProjectLabel[];
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
    case 10:
      return "Viewer";
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
  /** 20 = admin, 15 = member, 10 = viewer, 5 = guest */
  role: number;
}

/** Role values accepted by the members endpoints. */
export const WORKSPACE_ROLES = [
  { value: 20, label: "Admin" },
  { value: 15, label: "Member" },
  { value: 10, label: "Viewer" },
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
  /** Project-structure membership; present on the detail endpoint when
   *  fetched with ?include_project_structure=1 (C19T3), and on every row
   *  of the list endpoint when fetched with ?include_project_structure=1
   *  (C21T2: batched server-side, one query). */
  project_structure?: IssueProjectStructure;
  sort_order: number;
  start_date?: string | null;
  target_date?: string | null;
  estimate_point_id?: string | null;
  is_draft: boolean;
  archived_at?: string | null;
  /** Soft-delete timestamp (C52T0); set ONLY on rows from the trash
   *  list endpoint (?deleted=1). Null/undefined = live. */
  deleted_at?: string | null;
  created_by: string;
  created_at: string;
  updated_at: string;
  assignees: IssueAssignee[];
  labels: IssueLabel[];
  /** Subtask rollup (C17T2); omitted on endpoints that don't select it. */
  subtask_progress?: SubtaskProgress | null;
  /** Seconds spent in the current workflow state (C70T1); present on the
   *  detail (and list) endpoints when the backend selects it.
   *  Null/undefined = unknown (the property row is hidden). */
  time_in_state_seconds?: number | null;
  /** RFC3339 timestamp of when the issue entered its current state
   *  (C70T1); detail (and list) endpoints only. */
  state_entered_at?: string;
  /** Linked PRs/commits (C17T0). Always present as an array on the detail
   *  endpoint (empty when nothing is linked); omitted on list rows. */
  external_refs?: ExternalRef[];
  /** Epic memberships (C71T0): [{id, name, archived_at}]; present on the
   *  detail endpoint (omitted when the issue has none). Archived epics
   *  stay in this list — the UI dims them, never drops them. */
  epics?: EpicRef[];
}

/** One linked PR or commit on an issue (C17T0), as returned by
 *  GET .../issues/{uuid}/refs and embedded on the issue detail
 *  response. state is one of "open" | "merged" | "closed". */
export interface ExternalRef {
  id: string;
  issue_id: string;
  provider: string;
  repo: string;
  ref_type: "pr" | "commit";
  ref_id: string;
  url: string;
  state: string;
  created_at: string;
}

/** A minimal id+name reference (C19T3): one row of the
 *  ?include_project_structure=1 attachment. */
export interface NamedRef {
  id: string;
  name: string;
}

/** Cycle/module/release membership of an issue; present on the detail
 *  endpoint when fetched with ?include_project_structure=1 (C19T3), and on
 *  every list row when the list is fetched with ?include_project_structure=1
 *  (C21T2: resolved batched server-side in one query).
 *  Feeds the Properties > Project structure group in the peek panel. */
export interface IssueProjectStructure {
  cycle?: NamedRef | null;
  modules: NamedRef[];
  release?: NamedRef | null;
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

/** One hit from GET /api/v1/search?q=: the issue plus the workspace and
 *  project context needed to link to it from anywhere (C18T0). */
export interface SearchIssueItem extends Issue {
  workspace_slug: string;
  project_identifier: string;
}

/** One page hit from GET /api/v1/search?q= (C66T0): the page plus the
 *  workspace and project context needed to link to it from anywhere. */
export interface PageSearchResult {
  id: string;
  title: string;
  project_identifier: string;
  workspace_slug: string;
}

/** Search envelope: {"results": [...], "next_cursor"?: "...",
 *  "page_results": [...], "page_next_cursor"?: "..."} (extended C66T0).
 *  page_results is [] when there are no page hits. */
export interface SearchIssuesResult {
  results: SearchIssueItem[];
  next_cursor?: string;
  page_results: PageSearchResult[];
  page_next_cursor?: string;
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

/**
 * One workspace-scoped project label (C76T0): reusable across projects
 * in the workspace (Plane "at the workspace level"). The taxonomy
 * endpoints return the full row (workspace_id, timestamps); the project
 * payloads embed the [{id,name,color}] chip subset.
 */
export interface ProjectLabel {
  id: string;
  name: string;
  color: string;
  workspace_id?: string;
  created_at?: string;
  updated_at?: string;
}

export interface ActorRef {
  id: string;
  name?: string | null;
  email: string;
}

/** One comment node with nested replies. Resolution (C32) is a
 *  root-only property: replies always carry nulls. */
export interface CommentNode {
  id: string;
  issue_id: string;
  parent_id?: string | null;
  actor: ActorRef;
  content: unknown;
  created_at: string;
  updated_at: string;
  /** When the thread was resolved; null while open (roots only). */
  resolved_at?: string | null;
  /** Who resolved the thread; null while open (roots only). */
  resolved_by?: ActorRef | null;
  /** Reaction groups (C67T0); the backend always embeds `[]` — never
   *  null or omitted — on ListComments. */
  reactions: ReactionGroup[];
  replies: CommentNode[];
}

/** One page comment (C18T2): flat thread, body is a TipTap doc. The
 *  author is null when the author's user row was deleted. */
export interface PageComment {
  id: string;
  page_id: string;
  author: ActorRef | null;
  body: unknown;
  created_at: string;
  updated_at: string;
}

/** Health indicator on a project status update (C34T0). */
export type ProjectUpdateStatus = "on_track" | "at_risk" | "off_track";

/** One project status update (C34T0): flat timeline per project.
 *  The body is plain text (NOT markdown) in v1. The author is null
 *  when the author's user row was deleted. */
export interface ProjectUpdate {
  id: string;
  project_id: string;
  author: ActorRef | null;
  status: ProjectUpdateStatus;
  body: string;
  created_at: string;
  updated_at: string;
}

/** One issue_activities row. */
export interface HistoryEntry {
  id: string;
  field: string;
  old_value?: unknown;
  new_value?: unknown;
  /** Seconds spent in the state that a `state_id` transition left
   *  (C70T1); absent on all other history entries. */
  time_in_state_seconds?: number | null;
  actor: ActorRef;
  created_at: string;
}

/** One issue_subscribers row (GET .../issues/{uuid}/subscribers). */
export interface Subscriber {
  user_id: string;
  name?: string | null;
  email: string;
}

/** GET .../issues/{uuid}/votes response (C19T3). */
export interface IssueVotes {
  count: number;
  voted: boolean;
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

/** POST .../intake/bulk-triage result envelope. The accepted / rejected /
 *  skipped arrays hold intake row ids in request order; errors carries
 *  the rows that could not be triaged with the reason. */
export interface IntakeBulkTriageItemError {
  intake_id: string;
  error: string;
}

export interface IntakeBulkTriageResult {
  accepted: string[];
  rejected: string[];
  skipped: string[];
  errors: IntakeBulkTriageItemError[];
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

/**
 * An epic (C71T0): a long-running container for work that spans cycles,
 * with archive (not delete) as the lifecycle and state-group progress.
 * Mirrors the backend Epic contract; the list endpoint ships no progress
 * (detail only).
 */
export interface Epic {
  id: string;
  project_id: string;
  name: string;
  description?: string | null;
  /** Set when archived; archived epics hide from the default list. */
  archived_at?: string | null;
  created_by?: string | null;
  issue_count: number;
  /** State-group progress; present on the detail endpoint only. */
  progress?: { total: number; by_group: Record<string, number> } | null;
  created_at: string;
  updated_at: string;
}

/**
 * An epic membership ref on issue detail (C71T0): [{id, name,
 * archived_at}]. Archived epics stay visible — the UI dims them instead
 * of dropping them.
 */
export interface EpicRef {
  id: string;
  name: string;
  archived_at?: string | null;
}

/** An epic linked to an initiative (C71T0), from GET .../initiatives/:id/epics. */
export interface InitiativeEpic {
  epic_id: string;
  epic_name: string;
  project_id: string;
  project_name: string;
  added_at: string;
}

/** A project release/milestone (C4T6). release_date is YYYY-MM-DD or null.
 *  progress_snapshot counts live issues by state group: triage, backlog,
 *  unstarted, started, completed, cancelled — all six groups always
 *  present even when zero (C38T0, same shape as Cycle). */
export interface Release {
  id: string;
  project_id: string;
  name: string;
  description: string;
  status: "planned" | "released";
  release_date?: string | null;
  issue_count: number;
  progress_snapshot: Record<string, number>;
  created_at: string;
  updated_at: string;
}

/**
 * GET .../releases/{id}/notes (C33T1): a point-in-time Markdown snapshot
 * of the release's issues, grouped by state group. `counts.unstarted`
 * counts the Backlog section (unstarted, triage, backlog, cancelled, or
 * unknown group), so completed + started + unstarted == total.
 * release_date is YYYY-MM-DD or null.
 */
export interface ReleaseNotes {
  release: {
    id: string;
    name: string;
    status: string;
    release_date: string | null;
    description: string;
  };
  markdown: string;
  counts: {
    completed: number;
    started: number;
    unstarted: number;
    total: number;
  };
}

/** One day of a cycle burndown. `remaining` is null for future days. */
export interface BurndownDay {
  date: string; // YYYY-MM-DD
  remaining: number | null;
  ideal: number;
}

// ---------- Initiatives (C27) ----------

/** A workspace-scoped initiative (program) grouping projects.
 *  Unlike cycles/modules it is workspace-scoped: member projects live
 *  in other workspaces is impossible; initiatives own no issues. */
export interface Initiative {
  id: string;
  workspace_id: string;
  name: string;
  description?: string | null;
  status: "active" | "completed" | "archived";
  created_by?: string | null;
  /** Number of member projects (shipped by the list endpoint). */
  project_count: number;
  created_at: string;
  updated_at: string;
}

/** One member project of an initiative. `labels` is the project's
 *  workspace project-label chips (C76T0): [{id,name,color}], [] never
 *  null — shipped in the same query, no per-project round trip. */
export interface InitiativeProject {
  project_id: string;
  project_name: string;
  /** Workspace project-label chips (C76T0): [{id,name,color}]. */
  labels: ProjectLabel[];
  added_at: string;
}

/** Per-project slice of an initiative rollup. */
export interface InitiativeRollupProject {
  project_id: string;
  project_name: string;
  total: number;
  done_count: number;
  completion_pct: number;
  by_group: Record<string, number>;
}

/** Progress rollup for an initiative (C27T0): one aggregate query on
 *  the backend — issue counts by state group across all member
 *  projects plus a per-project breakdown. completion_pct is
 *  done-group (completed/cancelled) issues over total, 0 when total
 *  is 0. */
export interface InitiativeRollup {
  initiative_id: string;
  total: number;
  done_count: number;
  completion_pct: number;
  by_group: Record<string, number>;
  projects: InitiativeRollupProject[];
}

// ---------- OKRs — Objectives + Key Results (C35) ----------

/** Status vocabulary for objectives (mirrors initiatives). */
export type ObjectiveStatus = "active" | "completed" | "archived";

/** One workspace-scoped objective (goal). Progress is COMPUTED
 *  server-side (mean of KR progress, 0 when no KRs) and never stored.
 *  `key_results` is populated only by the detail endpoint.
 *  `progress_history` is the objective's progress-over-time series
 *  (one point per distinct metric-KR check-in timestamp, capped at
 *  the last 60 points); empty when no metric-KR check-in exists. */
export interface Objective {
  id: string;
  workspace_id: string;
  title: string;
  description?: string | null;
  /** Owner; null when unassigned. */
  owner?: ActorRef | null;
  start_date?: string | null; // YYYY-MM-DD
  target_date?: string | null; // YYYY-MM-DD
  status: ObjectiveStatus;
  /** Mean of KR progress, 0–1 (0 when no KRs). */
  progress: number;
  /** Progress-over-time series, 0–1 per point. Omitted when empty. */
  progress_history?: ObjectiveProgressPoint[];
  key_result_count: number;
  key_results?: KeyResult[];
  created_at: string;
  updated_at: string;
}

/** One point of an objective's progress-over-time series: the
 *  objective's computed progress (0–1) at timestamp `t`. */
export interface ObjectiveProgressPoint {
  t: string;
  progress: number;
}

/** KR kind: metric (start → target → current, manual) or issue
 *  (progress auto-derived from linked issues' done-group completion). */
export type KeyResultKind = "metric" | "issue";

/** A live issue linked to an issue KR — enough for the detail UI. */
export interface LinkedIssue {
  id: string;
  display_id: string;
  title: string;
  state_group: string;
}

/** One measurable key result under an objective. Progress is COMPUTED
 *  server-side: metric KRs clamp((current−start)/(target−start), 0, 1);
 *  issue KRs are done-group linked issues ÷ linked issues (0 when no
 *  links). `linked_issues` is populated only by the detail endpoint,
 *  for issue KRs. */
export interface KeyResult {
  id: string;
  objective_id: string;
  title: string;
  kind: KeyResultKind;
  /** Metric only. */
  unit?: string | null;
  start_value: number;
  target_value: number;
  /** Metric only; issue KRs always carry 0. */
  current_value: number;
  /** Computed progress, 0–1. */
  progress: number;
  linked_issues?: LinkedIssue[];
  created_at: string;
  updated_at: string;
}

/** Author of a KR check-in entry: id + display name (users.name
 *  falling back to users.email). Null when the author's user row was
 *  deleted. */
export interface CheckinAuthor {
  id: string;
  display_name: string;
}

/** One append-only KR check-in entry: a metric KR's current_value
 *  moving from previous_value to new_value, with the actor and an
 *  optional note. previous_value is null on the seed entry written
 *  at KR creation. There is no update/delete path. */
export interface KeyResultCheckin {
  id: string;
  key_result_id: string;
  author: CheckinAuthor | null;
  previous_value: number | null;
  new_value: number;
  note: string | null;
  created_at: string;
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
  /** Embedded on GET page detail (C24T3); omitted on list/create/update. */
  reactions?: ReactionGroup[];
}

/** One reaction group (C24T3): an emoji, its total count, and whether
 *  the caller reacted — the same shape as GET issue reactions. */
export interface ReactionGroup {
  emoji: string;
  count: number;
  reacted: boolean;
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

/** One page↔issue link row (C36T0). The link id itself is not exposed
 *  by the list endpoints — unlink addresses the pair (pageID, issueID). */
export interface PageIssueLink {
  id: string;
  page_id: string;
  issue_id: string;
  created_by?: string | null;
  created_at: string;
}

/**
 * One linked issue on the page side (GET .../pages/{pageID}/issues):
 * display_id, title, state name, priority — ordered by issue
 * created_at. `state` is the state's display name (no color — use a
 * plain badge, not StateBadge, which needs the full IssueState).
 */
export interface PageLinkedIssue {
  id: string;
  display_id: string;
  title: string;
  state: string;
  state_id: string;
  priority: number;
}

/**
 * One linked page on the issue side (GET .../issues/{uuid}/pages):
 * title + updated_at — ordered by page updated_at desc.
 */
export interface PageLinkedPage {
  id: string;
  title: string;
  updated_at: string;
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
