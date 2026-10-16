// Workspace audit log client (C37T1). Reads the append-only,
// workspace-scoped control-plane log served by C37T0:
//
//   GET /api/v1/workspaces/{slug}/audit-log
//   GET /api/v1/workspaces/{slug}/audit-log/export?format=csv
//
// Both endpoints are workspace-Admin-only (role 20) server-side; the
// Settings page additionally hides this section from non-admins.
//
// Action catalogue: exactly the 14 actions the backend emits. The
// backend does NOT emit integration.token_created /
// integration.token_revoked (API tokens are user-scoped, no workspace
// attribution — C37T0 accepted deviation), so token actions are NOT
// listed here and "token" is NOT an entity-type option either.
//
// Category: the backend has NO category query param — `action` is an
// exact match. Category is a UI-level grouping: it narrows which
// actions the Action dropdown offers, and the server filter is the
// chosen action. A category with no specific action sends no action
// param (the list is unfiltered by action).

import { api } from "./api";
import type { AdminPage } from "./admin";

export interface WorkspaceAuditEntry {
  id: string;
  at: string;
  actor_id: string | null;
  actor_email: string | null;
  action: string;
  entity_type: string;
  entity_id: string;
  ip: string | null;
  meta: Record<string, unknown>;
}

export type WorkspaceAuditCategory =
  | "member"
  | "project"
  | "workspace"
  | "integration";

export const WORKSPACE_AUDIT_CATEGORIES: WorkspaceAuditCategory[] = [
  "member",
  "project",
  "workspace",
  "integration",
];

/** The 14 actions the backend emits, grouped by scope. No token actions. */
export const WORKSPACE_AUDIT_ACTIONS: Record<
  WorkspaceAuditCategory,
  string[]
> = {
  member: [
    "member.invited",
    "member.added",
    "member.role_changed",
    "member.removed",
  ],
  project: [
    "project.created",
    "project.archived",
    "project.restored",
    "project.deleted",
  ],
  workspace: ["workspace.settings_updated"],
  integration: [
    "integration.webhook_created",
    "integration.webhook_deleted",
    "integration.github_configured",
    "integration.slack_configured",
    "integration.slack_disconnected",
  ],
};

export const WORKSPACE_AUDIT_ALL_ACTIONS: string[] = Object.values(
  WORKSPACE_AUDIT_ACTIONS,
).flat();

/** Entity types the backend emits (openapi WorkspaceAuditEntry). */
export const WORKSPACE_AUDIT_ENTITY_TYPES = [
  "invite",
  "member",
  "project",
  "workspace",
  "webhook",
  "integration",
];

export const WORKSPACE_AUDIT_PER_PAGE = 25;

/**
 * Draft/applied filter state. `since`/`until` hold the raw
 * `datetime-local` input values ("YYYY-MM-DDTHH:mm", local time) and
 * are converted to RFC 3339 UTC when the query string is built.
 */
export interface WorkspaceAuditFilters {
  category?: WorkspaceAuditCategory;
  action?: string;
  actor?: string;
  entityType?: string;
  since?: string;
  until?: string;
}

/** Category of a dotted `scope.verb` action: the part before the dot. */
export function auditCategory(action: string): string {
  const dot = action.indexOf(".");
  return dot === -1 ? action : action.slice(0, dot);
}

/**
 * Convert a `datetime-local` input value to RFC 3339 UTC
 * ("2026-10-16T19:00" -> "2026-10-16T11:00:00.000Z" in UTC+8).
 * Returns undefined for empty/invalid input — the caller drops it
 * from the query string so the server treats it as "no filter".
 */
export function datetimeLocalToISO(value: string | undefined): string | undefined {
  if (!value) return undefined;
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return undefined;
  return d.toISOString();
}

/**
 * Query params for the list/export endpoints. Empty filter fields are
 * dropped — the server treats them as "no filter". Category is never
 * sent (no backend param; see the module header).
 */
export function workspaceAuditParams(
  filters: WorkspaceAuditFilters,
): URLSearchParams {
  const params = new URLSearchParams();
  if (filters.action) params.set("action", filters.action);
  if (filters.actor) params.set("actor", filters.actor);
  if (filters.entityType) params.set("entity_type", filters.entityType);
  const since = datetimeLocalToISO(filters.since);
  if (since) params.set("since", since);
  const until = datetimeLocalToISO(filters.until);
  if (until) params.set("until", until);
  return params;
}

/** Full list URL: page/per_page + the applied filters. */
export function buildWorkspaceAuditLogUrl(
  slug: string,
  page: number,
  filters: WorkspaceAuditFilters,
  perPage = WORKSPACE_AUDIT_PER_PAGE,
): string {
  const params = workspaceAuditParams(filters);
  params.set("page", String(page));
  params.set("per_page", String(perPage));
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/audit-log?${params.toString()}`;
}

/**
 * Export URL: same filter set as the list, `format=csv`, no
 * pagination params (the export endpoint ignores them; it caps at
 * 10,000 rows server-side and signals truncation via X-Truncated).
 * The download filename comes from the server's Content-Disposition
 * header (audit-log-<slug>-<YYYY-MM-DD>.csv).
 */
export function buildWorkspaceAuditExportUrl(
  slug: string,
  filters: WorkspaceAuditFilters,
): string {
  const params = workspaceAuditParams(filters);
  params.set("format", "csv");
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/audit-log/export?${params.toString()}`;
}

/** Reads one page of the audit log, newest first (server order). */
export function fetchWorkspaceAuditLog(
  slug: string,
  page = 1,
  filters: WorkspaceAuditFilters = {},
  perPage = WORKSPACE_AUDIT_PER_PAGE,
): Promise<AdminPage<WorkspaceAuditEntry>> {
  return api.get<AdminPage<WorkspaceAuditEntry>>(
    buildWorkspaceAuditLogUrl(slug, page, filters, perPage),
  );
}

/**
 * Short display form for ids: long opaque ids are truncated to 8
 * chars + ellipsis (the Admin audit tab's convention). Emails (invite
 * entity ids) are shown in full — truncating them would hide the
 * informative part.
 */
export function truncateAuditId(id: string): string {
  if (id.includes("@")) return id;
  return id.length > 8 ? `${id.slice(0, 8)}…` : id;
}
