// versions: issue version-history client + snapshot diff helper (C53T0).
//
// Backend contract (fixed, verified against
// internal/api/issue_handler.go:1163-1165 + internal/service/satellite.go):
//   GET  .../issues/{uuid}/versions            -> 200 {versions: IssueVersion[]}, oldest first.
//   GET  .../issues/{uuid}/versions/{n}        -> 200 IssueVersion. n < 1 -> 400; unknown n -> 404.
//   POST .../issues/{uuid}/versions/{n}/restore -> 200 Issue (the post-restore
//        live row). Applies the old snapshot as a NEW version — history is
//        never rewritten. Restore requires role >= 15; guests get 403.
//   IssueVersion = {version_no, snapshot: <full Issue JSON>,
//        created_by: {id, name, email}, created_at}. The snapshot is the Go
//        Issue struct; deleted_at is nil in snapshots (scanIssue never
//        selects it) and subtask_progress is absent from snapshots.
//
// Diff rules (C53T0): scalar fields only. Labels/assignees/custom values
// are NOT in the snapshot (the Go Issue struct carries none of them), so
// they are never diffed — see the one-line note the UI renders under the
// diff. description is compared by canonical JSON and reported as
// changed / not-changed only — never a word-level diff, never a preview.

import { api } from "./api";
import type { ActorRef, Issue } from "./types";

function base(slug: string, identifier: string): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}/issues`;
}

/** One immutable snapshot of an issue row, oldest first from the list endpoint. */
export interface IssueVersion {
  version_no: number;
  snapshot: Issue;
  /** Reuses ActorRef: the backend name is a nullable pointer (users.name
   *  falling back to email), so name is `string | null | undefined` here,
   *  not the plan shorthand's plain string. */
  created_by: ActorRef;
  created_at: string;
}

/** Fetch all versions of an issue, oldest first (the API order). */
export function fetchVersions(
  slug: string,
  identifier: string,
  issueId: string,
): Promise<IssueVersion[]> {
  return api
    .get<{ versions: IssueVersion[] }>(
      `${base(slug, identifier)}/${encodeURIComponent(issueId)}/versions`,
    )
    .then((r) => r.versions ?? []);
}

/** Fetch one version by its 1-based version number. */
export function fetchVersion(
  slug: string,
  identifier: string,
  issueId: string,
  n: number,
): Promise<IssueVersion> {
  return api.get<IssueVersion>(
    `${base(slug, identifier)}/${encodeURIComponent(issueId)}/versions/${n}`,
  );
}

/**
 * Restore a version: applies the old snapshot as a NEW version and
 * returns the post-restore live issue row. No request body.
 */
export function restoreVersion(
  slug: string,
  identifier: string,
  issueId: string,
  n: number,
): Promise<Issue> {
  return api.post<Issue>(
    `${base(slug, identifier)}/${encodeURIComponent(issueId)}/versions/${n}/restore`,
  );
}

export function versionsKey(slug: string, identifier: string, issueId: string) {
  return ["versions", slug, identifier, issueId] as const;
}

// ---------- Snapshot diff ----------

/** One changed field between two snapshots. For `description`,
 *  oldValue/newValue carry the raw TipTap docs — consumers must render
 *  "changed" only, never a word diff or content preview. */
export interface VersionChange {
  field: string;
  oldValue: unknown;
  newValue: unknown;
}

/** Scalar fields compared by diffSnapshots, in stable report order. */
const COMPARED_FIELDS = [
  "name",
  "description",
  "priority",
  "state_id",
  "parent_id",
  "sort_order",
  "start_date",
  "target_date",
  "estimate_point_id",
  "is_draft",
  "archived_at",
] as const;

type ComparedField = (typeof COMPARED_FIELDS)[number];

/**
 * Canonical JSON stringify: object keys sorted recursively, so key
 * reorder in a description doc never false-positives as a change.
 * Returns undefined for undefined (JSON.stringify parity).
 */
export function stableStringify(value: unknown): string | undefined {
  if (value === null || typeof value !== "object") {
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    const items = value.map((item) => stableStringify(item) ?? "null");
    return `[${items.join(",")}]`;
  }
  const obj = value as Record<string, unknown>;
  const keys = Object.keys(obj).sort();
  const parts = keys.map((k) => {
    const v = stableStringify(obj[k]);
    // JSON.stringify drops undefined-valued keys; match that.
    return v === undefined ? null : `${JSON.stringify(k)}:${v}`;
  });
  return `{${parts.filter((p) => p !== null).join(",")}}`;
}

/** Scalar equality with null/undefined treated as the same "absent". */
function scalarEqual(a: unknown, b: unknown): boolean {
  if (a == null && b == null) return true;
  return a === b;
}

function fieldValue(issue: Issue, field: ComparedField): unknown {
  return issue[field];
}

/**
 * Diff two snapshots. Skipped (never reported), per the C50
 * content-comparison exclusions in satellite.go:issueRowContentEqual:
 * identity fields (id, project_id, sequence_id, display_id, created_by,
 * created_at), updated_at (every snapshot differs), subtask_progress
 * (derived rollup), deleted_at (nil in snapshots).
 * `prev = null` (v1) returns the created marker — the v1 row is the
 * creation snapshot, not a change.
 */
export function diffSnapshots(
  prev: Issue | null,
  curr: Issue,
): VersionChange[] {
  if (prev === null) {
    return [{ field: "created", oldValue: null, newValue: null }];
  }
  const changes: VersionChange[] = [];
  for (const field of COMPARED_FIELDS) {
    const oldValue = fieldValue(prev, field);
    const newValue = fieldValue(curr, field);
    if (field === "description") {
      // TipTap JSON doc: nullish (absent vs null) is the same empty doc.
      const oldDoc = oldValue ?? null;
      const newDoc = newValue ?? null;
      if (stableStringify(oldDoc) !== stableStringify(newDoc)) {
        changes.push({ field, oldValue: oldDoc, newValue: newDoc });
      }
      continue;
    }
    if (!scalarEqual(oldValue, newValue)) {
      changes.push({ field, oldValue: oldValue ?? null, newValue: newValue ?? null });
    }
  }
  return changes;
}
