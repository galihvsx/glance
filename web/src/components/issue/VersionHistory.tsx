// VersionHistory (C53T1): the Versions tab in issue detail — the version
// snapshots cycles 49–52 made authoritative, finally visible.
//
// List newest-first (the API returns oldest-first; reversing client-side
// matches the activity feed, which is also newest-first, and puts the
// current state on top). Each row shows v{version_no} + author +
// relative time + changed-field chips (diffSnapshots vs the previous
// version; v1 shows a "Created" chip). Expanding a row shows the
// field diff table plus the one-line snapshot-limitation note.
//
// Restore: per-row button, hidden on the latest version and visible
// only to writers (role >= 15, same precedent as
// IssueCustomFields.tsx:344 — default 0 = hidden until the role
// resolves). The button is hidden on the latest version because a
// restore of the current snapshot is a no-op: the backend rejects it
// with 409 (ErrVersionRestoreNoOp — no version row, no _restored
// marker since C64T0). Click → confirm dialog → restoreVersion → on
// dialog → restoreVersion → on success prompt cache invalidation
// (versions + issue + issues + board-issues + history, following the
// C52T2 Trash.tsx precedent: no optimistic cache writes) and the new
// version appears as latest. On 403 → permission message; on 404 →
// refetch the list (defensive — versions are immutable).
//
// Field rendering: human labels; state_id → state name via the
// already-loaded project states; priority → priorityLabel; parent_id →
// "parent changed" + display id if already loaded (via the
// parentDisplayIds map the detail page builds from data it already
// holds), else truncated UUID — NEVER a per-row fetch; description →
// "changed" with no content preview (rich-text JSON is not
// previewable in a diff row); dates as plain dates; booleans as
// Yes/No.

import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, api } from "../../lib/api";
import {
  diffSnapshots,
  fetchVersions,
  restoreVersion,
  versionsKey,
  type IssueVersion,
  type VersionChange,
} from "../../lib/versions";
import { relativeTime } from "../../lib/relativeTime";
import {
  priorityLabel,
  type IssueState,
  type Workspace,
} from "../../lib/types";
import { Skeleton } from "../ui/skeleton";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "../ui/alert-dialog";
import { cn } from "cn";

const FIELD_LABELS: Record<string, string> = {
  created: "Created",
  name: "Name",
  description: "Description",
  priority: "Priority",
  state_id: "State",
  parent_id: "Parent",
  sort_order: "Sort order",
  start_date: "Start date",
  target_date: "Target date",
  estimate_point_id: "Estimate",
  is_draft: "Draft",
  archived_at: "Archived",
};

function fieldLabel(field: string): string {
  return FIELD_LABELS[field] ?? field;
}

/** First 8 chars of an id + ellipsis — enough to disambiguate in a diff. */
function truncateId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id;
}

/** Plain YYYY-MM-DD date from an ISO timestamp or date string. */
function plainDate(v: unknown): string {
  if (typeof v !== "string" || v === "") return "—";
  return v.split("T")[0];
}

function formatValue(
  field: string,
  value: unknown,
  statesById: ReadonlyMap<string, string>,
  parentDisplayIds: ReadonlyMap<string, string>,
): string {
  if (value == null) {
    return field === "parent_id" ? "No parent" : "—";
  }
  switch (field) {
    case "state_id":
      return statesById.get(String(value)) ?? truncateId(String(value));
    case "priority": {
      const n = Number(value);
      return Number.isNaN(n) ? String(value) : priorityLabel(n);
    }
    case "parent_id":
      return parentDisplayIds.get(String(value)) ?? truncateId(String(value));
    case "description":
      // Rich-text JSON: report "changed" only, never a preview.
      return "changed";
    case "start_date":
    case "target_date":
    case "archived_at":
      return plainDate(value);
    case "is_draft":
      return value ? "Yes" : "No";
    case "estimate_point_id":
      return truncateId(String(value));
    default:
      return String(value);
  }
}

export default function VersionHistory({
  slug,
  identifier,
  uuid,
  states,
  parentDisplayIds,
}: {
  slug: string;
  identifier: string;
  uuid: string;
  states: IssueState[];
  /** Display ids for parents the detail page already loaded (the live
   *  parent). Any other parent_id renders as a truncated UUID — the
   *  component never fetches per-row. */
  parentDisplayIds?: ReadonlyMap<string, string>;
}) {
  const queryClient = useQueryClient();
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const [confirmN, setConfirmN] = useState<number | null>(null);
  const [restoreError, setRestoreError] = useState<string | null>(null);
  const [restoreSuccess, setRestoreSuccess] = useState<string | null>(null);

  const versionsQuery = useQuery({
    queryKey: versionsKey(slug, identifier, uuid),
    queryFn: () => fetchVersions(slug, identifier, uuid),
  });

  // Member (15)+ may restore; guests read. Server is the gate.
  const roleQuery = useQuery({
    queryKey: ["workspace-role", slug],
    queryFn: () =>
      api
        .get<Workspace & { role: number }>(
          `/api/v1/workspaces/${encodeURIComponent(slug)}`,
        )
        .then((w) => w.role ?? 0),
    staleTime: 60_000,
  });
  const canRestore = (roleQuery.data ?? 0) >= 15;

  const versions = versionsQuery.data ?? [];
  const statesById = useMemo(
    () => new Map(states.map((s) => [s.id, s.name])),
    [states],
  );
  const parentIds = parentDisplayIds ?? EMPTY_MAP;

  // Newest-first for display; the API order (oldest-first) is kept for
  // diffing, where "previous" means the next-older version. The memos
  // depend on versionsQuery.data itself (stable identity), not on the
  // `?? []` fallback, which would re-evaluate every render.
  const newestFirst = useMemo(
    () => [...(versionsQuery.data ?? [])].reverse(),
    [versionsQuery.data],
  );
  const changesByVersion = useMemo(() => {
    const m = new Map<number, VersionChange[]>();
    const rows = versionsQuery.data ?? [];
    rows.forEach((v, i) => {
      const prev = i === 0 ? null : rows[i - 1].snapshot;
      m.set(v.version_no, diffSnapshots(prev, v.snapshot));
    });
    return m;
  }, [versionsQuery.data]);
  // Oldest-first array: the last element is the latest version.
  const latestVersionNo =
    versions.length > 0 ? versions[versions.length - 1].version_no : null;

  const restoreMutation = useMutation({
    mutationFn: (n: number) => restoreVersion(slug, identifier, uuid, n),
    onSuccess: () => {
      setConfirmN(null);
      setRestoreError(null);
      setRestoreSuccess(
        "Version restored. A new version now appears as latest.",
      );
      void queryClient.invalidateQueries({
        queryKey: versionsKey(slug, identifier, uuid),
      });
      void queryClient.invalidateQueries({
        queryKey: ["issue", slug, identifier, uuid],
      });
      void queryClient.invalidateQueries({
        queryKey: ["issues", slug, identifier],
      });
      void queryClient.invalidateQueries({
        queryKey: ["board-issues", slug, identifier],
      });
      void queryClient.invalidateQueries({
        queryKey: ["history", slug, identifier, uuid],
      });
    },
    onError: (e) => {
      setConfirmN(null);
      setRestoreSuccess(null);
      if (e instanceof ApiError && e.status === 403) {
        setRestoreError("You don't have permission to restore versions");
      } else if (e instanceof ApiError && e.status === 404) {
        // Defensive only — versions are immutable, so a 404 means the
        // version vanished out from under us. Refresh the list.
        setRestoreError(
          "That version no longer exists — the list has been refreshed.",
        );
        void queryClient.invalidateQueries({
          queryKey: versionsKey(slug, identifier, uuid),
        });
      } else {
        setRestoreError(
          e instanceof Error ? e.message : "Failed to restore version",
        );
      }
    },
  });

  function toggleExpand(n: number) {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(n)) next.delete(n);
      else next.add(n);
      return next;
    });
  }

  if (versionsQuery.isPending) {
    return (
      <div className="space-y-2 py-4">
        <Skeleton className="h-12 w-full" />
        <Skeleton className="h-12 w-full" />
      </div>
    );
  }
  if (versionsQuery.isError) {
    return (
      <div className="py-6">
        <p className="text-[10px] text-ink-muted">Failed to load versions.</p>
        <button
          type="button"
          onClick={() => void versionsQuery.refetch()}
          className="mt-2 text-[10px] text-ink-muted hover:text-ink hover:underline"
        >
          Retry
        </button>
      </div>
    );
  }
  if (versions.length === 0) {
    return <p className="py-6 text-[10px] text-ink-muted">No versions yet.</p>;
  }

  return (
    <div>
      {restoreError && (
        <p role="alert" className="py-2 text-[10px] text-accent-red">
          {restoreError}
        </p>
      )}
      {restoreSuccess && (
        <p role="status" className="py-2 text-[10px] text-accent-green">
          {restoreSuccess}
        </p>
      )}
      <div className="divide-y divide-border-hairline">
        {newestFirst.map((v: IssueVersion) => {
          const changes = changesByVersion.get(v.version_no) ?? [];
          const isLatest = v.version_no === latestVersionNo;
          const isExpanded = expanded.has(v.version_no);
          const author = v.created_by.name ?? v.created_by.email;
          const initial = (author[0] ?? "?").toUpperCase();
          const chips =
            changes.length === 0
              ? ["No changes"]
              : changes.map((c) => fieldLabel(c.field));
          return (
            <div
              key={v.version_no}
              className="grid grid-cols-[27px_1fr] gap-[9px] py-[17px]"
            >
              <span
                className="flex h-[25px] w-[25px] items-center justify-center rounded-full border border-border bg-raised text-[10px] font-semibold text-ink-secondary"
                aria-hidden
              >
                {initial}
              </span>
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <span className="text-[10px] font-semibold text-ink">
                    v{v.version_no}
                  </span>
                  <span className="text-[10px] text-ink-secondary">
                    {author}
                  </span>
                  <span className="text-[9px] text-ink-muted">
                    {relativeTime(v.created_at)}
                  </span>
                  {isLatest && (
                    <span className="rounded-full border border-border bg-raised px-1.5 py-px text-[9px] text-ink-secondary">
                      Latest
                    </span>
                  )}
                </div>
                <div className="mt-1.5 flex flex-wrap gap-1">
                  {chips.map((chip) => (
                    <span
                      key={chip}
                      className={cn(
                        "rounded-full border border-border bg-raised px-1.5 py-px text-[9px] text-ink-secondary",
                        chip === "Created" &&
                          "border-accent-green/40 bg-accent-green/10 text-accent-green",
                      )}
                    >
                      {chip}
                    </span>
                  ))}
                </div>
                <div className="mt-2 flex items-center gap-3">
                  <button
                    type="button"
                    onClick={() => toggleExpand(v.version_no)}
                    className="text-[9px] text-ink-muted hover:text-ink hover:underline"
                  >
                    {isExpanded ? "Hide diff" : "Show diff"}
                  </button>
                  {canRestore && !isLatest && (
                    <button
                      type="button"
                      onClick={() => {
                        setRestoreError(null);
                        setRestoreSuccess(null);
                        setConfirmN(v.version_no);
                      }}
                      className="flex h-[25px] items-center rounded-[6px] border border-border bg-surface px-[7px] text-[10px] text-ink-secondary transition-colors hover:bg-raised hover:text-ink"
                    >
                      Restore
                    </button>
                  )}
                </div>
                {isExpanded &&
                  (changes.length === 0 ? (
                    <p className="mt-3 text-[10px] text-ink-muted">
                      No field changes in this version.
                    </p>
                  ) : (
                    <div className="mt-3 overflow-hidden rounded-[7px] border border-border">
                      <table className="w-full text-[10px]">
                        <thead>
                          <tr className="bg-raised text-left text-ink-muted">
                            <th className="px-2.5 py-1.5 font-medium">Field</th>
                            <th className="px-2.5 py-1.5 font-medium">
                              Before
                            </th>
                            <th className="px-1 py-1.5" aria-hidden />
                            <th className="px-2.5 py-1.5 font-medium">
                              After
                            </th>
                          </tr>
                        </thead>
                        <tbody>
                          {changes.map((c) =>
                            c.field === "description" ? (
                              <tr
                                key={c.field}
                                className="border-t border-border-hairline"
                              >
                                <td className="px-2.5 py-1.5 text-ink-secondary">
                                  Description
                                </td>
                                <td
                                  colSpan={3}
                                  className="px-2.5 py-1.5 text-ink-muted"
                                >
                                  changed
                                </td>
                              </tr>
                            ) : (
                              <tr
                                key={c.field}
                                className="border-t border-border-hairline"
                              >
                                <td className="px-2.5 py-1.5 text-ink-secondary">
                                  {fieldLabel(c.field)}
                                </td>
                                <td className="px-2.5 py-1.5 text-ink-muted">
                                  {formatValue(
                                    c.field,
                                    c.oldValue,
                                    statesById,
                                    parentIds,
                                  )}
                                </td>
                                <td
                                  className="px-1 py-1.5 text-ink-muted"
                                  aria-hidden
                                >
                                  →
                                </td>
                                <td className="px-2.5 py-1.5 text-ink">
                                  {formatValue(
                                    c.field,
                                    c.newValue,
                                    statesById,
                                    parentIds,
                                  )}
                                </td>
                              </tr>
                            ),
                          )}
                        </tbody>
                      </table>
                      <p className="px-2.5 pt-1 pb-2 text-[9px] text-ink-muted">
                        Labels, assignees and custom values are not part of
                        version snapshots.
                      </p>
                    </div>
                  ))}
              </div>
            </div>
          );
        })}
      </div>

      <AlertDialog
        open={confirmN !== null}
        onOpenChange={(o) => {
          if (!o) setConfirmN(null);
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              Restore version {confirmN}?
            </AlertDialogTitle>
            <AlertDialogDescription>
              This applies the old snapshot as a new version; history is
              never rewritten.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              disabled={restoreMutation.isPending}
              onClick={() => {
                if (confirmN !== null) restoreMutation.mutate(confirmN);
              }}
            >
              {restoreMutation.isPending ? "Restoring…" : "Restore"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

const EMPTY_MAP: ReadonlyMap<string, string> = new Map();
