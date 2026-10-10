// Admin API client (C5T1). All routes live under /api/v1/admin and are
// gated server-side by RequireAdmin (internal/api/admin_handler.go, C5T0);
// the UI additionally hides the nav entry and redirects non-admins away
// from /admin so nobody sees a dead end.
//
// Paginated envelopes share the {items, total, page, per_page} shape and
// default to 25 rows per page, matching the server's pageParams contract.

import { api } from "./api";
import type { User } from "./auth";

export interface AdminStats {
  users: number;
  workspaces: number;
  projects: number;
  issues: number;
  attachment_bytes: number;
}

export interface AdminUser {
  id: string;
  email: string;
  name: string | null;
  is_admin: boolean;
  is_active: boolean;
  workspace_count: number;
  created_at: string;
}

export interface AdminWorkspace {
  id: string;
  slug: string;
  name: string;
  member_count: number;
  project_count: number;
  issue_count: number;
  created_at: string;
}

export interface AdminAuditEntry {
  id: string;
  at: string;
  actor_id: string | null;
  actor_email: string | null;
  action: string;
  entity_type: string;
  entity_id: string;
  workspace_id: string | null;
  ip: string | null;
  meta: Record<string, unknown>;
}

export interface AdminAuditFilters {
  action?: string;
  actorId?: string;
  entityType?: string;
}

export interface AdminBackupConfig {
  enabled: boolean;
  interval: string;
  dir: string;
  retention: number;
}

export interface AdminBackupRun {
  id: string;
  workspace_id: string | null;
  workspace_slug: string;
  at: string;
  file_name: string;
  file_path: string | null;
  byte_size: number;
  sha256: string;
  format_version: string;
  migration_version: string;
  status: "ok" | "failed" | "pruned";
  verify_ok: boolean | null;
  verify_error: string | null;
  verified_at: string | null;
  triggered_by: string;
}

export interface AdminBackupsResponse extends AdminPage<AdminBackupRun> {
  config: AdminBackupConfig;
}

export interface AdminPage<T> {
  items: T[];
  total: number;
  page: number;
  per_page: number;
}

export const ADMIN_KEYS = {
  stats: ["admin", "stats"] as const,
  users: (page: number) => ["admin", "users", page] as const,
  workspaces: (page: number) => ["admin", "workspaces", page] as const,
  auditLog: (page: number, filters: AdminAuditFilters) =>
    ["admin", "audit-log", page, filters] as const,
  backups: (page: number, workspaceSlug?: string) =>
    ["admin", "backups", page, workspaceSlug ?? ""] as const,
};

export const ADMIN_PER_PAGE = 25;

export function fetchAdminStats(): Promise<AdminStats> {
  return api.get<AdminStats>("/api/v1/admin/stats");
}

export function fetchAdminUsers(
  page = 1,
  perPage = ADMIN_PER_PAGE,
): Promise<AdminPage<AdminUser>> {
  return api.get<AdminPage<AdminUser>>(
    `/api/v1/admin/users?page=${page}&per_page=${perPage}`,
  );
}

/** Flips the instance-admin flag. Rejects when is_admin is missing — the
 *  client always says what it wants explicitly, like the server demands. */
export async function setAdminUser(id: string, isAdmin: boolean): Promise<void> {
  await api.patch<{ ok: boolean }>(
    `/api/v1/admin/users/${encodeURIComponent(id)}`,
    { is_admin: isAdmin },
  );
}

export async function deactivateAdminUser(id: string): Promise<void> {
  await api.post<{ ok: boolean }>(
    `/api/v1/admin/users/${encodeURIComponent(id)}/deactivate`,
  );
}

export async function reactivateAdminUser(id: string): Promise<void> {
  await api.post<{ ok: boolean }>(
    `/api/v1/admin/users/${encodeURIComponent(id)}/reactivate`,
  );
}

export function fetchAdminWorkspaces(
  page = 1,
  perPage = ADMIN_PER_PAGE,
): Promise<AdminPage<AdminWorkspace>> {
  return api.get<AdminPage<AdminWorkspace>>(
    `/api/v1/admin/workspaces?page=${page}&per_page=${perPage}`,
  );
}

/** Deletes a workspace. The name typed into the confirmation dialog is
 *  passed verbatim as ?confirm= — the server rejects a mismatch (409), so
 *  the client never invents its own notion of "confirmed". */
export async function deleteAdminWorkspace(
  id: string,
  confirmName: string,
): Promise<void> {
  await api.del(
    `/api/v1/admin/workspaces/${encodeURIComponent(id)}?confirm=${encodeURIComponent(confirmName)}`,
  );
}

/** Reads the append-only audit log. Empty filter fields are dropped from
 *  the query string — the server treats them as "no filter". */
export function fetchAdminAuditLog(
  page = 1,
  filters: AdminAuditFilters = {},
  perPage = ADMIN_PER_PAGE,
): Promise<AdminPage<AdminAuditEntry>> {
  const params = new URLSearchParams({
    page: String(page),
    per_page: String(perPage),
  });
  if (filters.action) params.set("action", filters.action);
  if (filters.actorId) params.set("actor_id", filters.actorId);
  if (filters.entityType) params.set("entity_type", filters.entityType);
  return api.get<AdminPage<AdminAuditEntry>>(
    `/api/v1/admin/audit-log?${params.toString()}`,
  );
}

/** Reads the backup run history (newest first) plus the
 *  schedule/retention config. An optional workspace slug scopes the
 *  history to one workspace. */
export function fetchAdminBackups(
  page = 1,
  workspaceSlug?: string,
  perPage = ADMIN_PER_PAGE,
): Promise<AdminBackupsResponse> {
  const params = new URLSearchParams({
    page: String(page),
    per_page: String(perPage),
  });
  if (workspaceSlug) params.set("workspace_slug", workspaceSlug);
  return api.get<AdminBackupsResponse>(
    `/api/v1/admin/backups?${params.toString()}`,
  );
}

/** Triggers a backup now. An empty slug backs up every workspace; a
 *  207 response means partial failure (some workspaces failed) — the
 *  successful runs are still returned. */
export function runAdminBackupNow(
  workspaceSlug?: string,
): Promise<{ backups: AdminBackupRun[]; error?: string }> {
  return api.post<{ backups: AdminBackupRun[]; error?: string }>(
    "/api/v1/admin/backups/run",
    workspaceSlug ? { workspace_slug: workspaceSlug } : {},
  );
}

/** Guard decision for the /admin route: what the router should do given
 *  the current auth state. Pure (no hooks) so it is unit-testable and the
 *  guard component stays a dumb switch. */
export type AdminRouteDecision = "loading" | "login" | "home" | "allow";

export function adminRouteDecision(
  loading: boolean,
  user: Pick<User, "is_admin"> | null,
): AdminRouteDecision {
  if (loading) return "loading";
  if (!user) return "login";
  if (!user.is_admin) return "home";
  return "allow";
}

/** Page count for a total row count at a fixed page size. Always ≥ 1 so
 *  "Page 1 of 1" renders even for an empty table. */
export function totalPages(total: number, perPage: number): number {
  return Math.max(1, Math.ceil(Math.max(0, total) / perPage));
}
