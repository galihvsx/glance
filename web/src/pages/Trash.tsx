// Trash view (C52T2): soft-deleted issues (deleted_at set) for the
// project, mirroring Archived.tsx.
//
// Actions per deleted issue: Restore (POST .../issues/:uuid/restore —
// non-destructive, so NO confirm dialog, single click + invalidation
// on success, same as Archived's Unarchive). Bulk: selection
// checkboxes + "Restore selected" (POST .../issues/bulk-restore).
// Titles render as PLAIN TEXT, not links: a still-deleted row's detail
// link would 404 (deleted rows read as ErrIssueNotFound everywhere
// except the trash list), so no link is offered until restored.
// "Deleted by" is not shown: the backend has no deleted_by field, only
// the deleted date — the same documented limitation as Archived's
// "Archived by". There is no hard/permanent delete anywhere in the
// app; Trash is restore-only.

import { useState } from "react";
import { useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../lib/api";
import {
  bulkRestoreIssues,
  fetchDeleted,
  restoreIssue,
  trashKey,
} from "../lib/trash";
import { relativeTime } from "../lib/relativeTime";
import PageHeader from "../components/shell/PageHeader";
import EmptyState from "../components/personal/EmptyState";
import { Alert, AlertDescription } from "../components/ui/alert";
import { Button } from "../components/ui/button";
import { Checkbox } from "../components/ui/checkbox";
import { Input } from "../components/ui/input";
import { Skeleton } from "../components/ui/skeleton";
import { Search, Trash2 } from "lucide-react";

function errMsg(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message : fallback;
}

export default function Trash() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const [bulkError, setBulkError] = useState<string | null>(null);

  const trashQuery = useQuery({
    queryKey: trashKey(slug, identifier),
    queryFn: () => fetchDeleted(slug, identifier),
    enabled: slug !== "" && identifier !== "",
  });

  const invalidate = () => {
    void queryClient.invalidateQueries({
      queryKey: trashKey(slug, identifier),
    });
    // The restored issue re-enters the working set: invalidate the
    // list/board caches. Prefix keys match the filtered variants
    // (Issues: ["issues", slug, identifier, filters, orderBy];
    // Board: ["board-issues", slug, identifier, filters]).
    // Drafts + calendar also re-admit a restored issue (a deleted
    // draft restores into Drafts; a dated issue back onto Calendar).
    void queryClient.invalidateQueries({
      queryKey: ["issues", slug, identifier],
    });
    void queryClient.invalidateQueries({
      queryKey: ["board-issues", slug, identifier],
    });
    void queryClient.invalidateQueries({
      queryKey: ["drafts", slug, identifier],
    });
    void queryClient.invalidateQueries({
      queryKey: ["calendar", slug, identifier],
    });
  };

  const restoreMutation = useMutation({
    mutationFn: (id: string) => restoreIssue(slug, identifier, id),
    onSuccess: (_data, id) => {
      setSelected((prev) => {
        if (!prev.has(id)) return prev;
        const next = new Set(prev);
        next.delete(id);
        return next;
      });
      invalidate();
    },
  });

  const bulkRestoreMutation = useMutation({
    mutationFn: (ids: string[]) => bulkRestoreIssues(slug, identifier, ids),
    onSuccess: (results) => {
      const failed = results.filter((r) => !r.ok);
      if (failed.length > 0) {
        setBulkError(
          `${failed.length} issue${failed.length === 1 ? "" : "s"} could not be restored: ${failed
            .map((f) => f.error ?? "issue not found")
            .join("; ")}`,
        );
      } else {
        setBulkError(null);
      }
      // Keep failed ids selected so they can be retried; drop the ones
      // that restored.
      setSelected((prev) => {
        const restored = new Set(
          results.filter((r) => r.ok).map((r) => r.id),
        );
        if ([...prev].every((id) => !restored.has(id))) return prev;
        return new Set([...prev].filter((id) => !restored.has(id)));
      });
      invalidate();
    },
  });

  const error =
    restoreMutation.error ?? bulkRestoreMutation.error ?? trashQuery.error;

  const deleted = trashQuery.data ?? [];

  // Client-side search filter over the (single-page, per_page=100)
  // trash list: name + display_id, case-insensitive.
  const query = search.trim().toLowerCase();
  // No useMemo: the list is a single page (per_page=100) and the filter
  // is a trivial string scan.
  const filtered =
    query === ""
      ? deleted
      : deleted.filter(
          (d) =>
            d.name.toLowerCase().includes(query) ||
            d.display_id.toLowerCase().includes(query),
        );

  const allFilteredSelected =
    filtered.length > 0 && filtered.every((d) => selected.has(d.id));

  const toggleSelect = (id: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const toggleSelectAll = () => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (allFilteredSelected) {
        for (const d of filtered) next.delete(d.id);
      } else {
        for (const d of filtered) next.add(d.id);
      }
      return next;
    });
  };

  const restoring =
    restoreMutation.isPending || bulkRestoreMutation.isPending;

  return (
    <div className="w-full">
      {/* C19T4: shared page header replaces the per-page h1 + ProjectNav. */}
      <PageHeader viewName="Trash" count={deleted.length} />
      <div className="mx-auto w-full max-w-5xl space-y-4 p-4">
        {error && (
          <Alert variant="destructive">
            <AlertDescription>
              {errMsg(error, "Something went wrong")}
            </AlertDescription>
          </Alert>
        )}
        {bulkError && (
          <Alert variant="destructive">
            <AlertDescription>{bulkError}</AlertDescription>
          </Alert>
        )}

        {trashQuery.isLoading ? (
          <div className="space-y-2">
            <Skeleton className="h-[47px] w-full" />
            <Skeleton className="h-[47px] w-full" />
          </div>
        ) : deleted.length === 0 ? (
          <EmptyState icon={Trash2} title="Trash is empty">
            <p>
              Deleted issues land here. Restore one to bring it back to
              the working set.
            </p>
          </EmptyState>
        ) : (
          <>
            <div className="flex flex-wrap items-center gap-2">
              <form
                className="relative"
                onSubmit={(e) => e.preventDefault()}
              >
                <Search className="absolute left-2 top-[8px] h-3.5 w-3.5 text-ink-muted" />
                <Input
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                  placeholder="Search deleted issues…"
                  aria-label="Search deleted issues"
                  className="h-[30px] w-56 pl-8 text-[11px]"
                />
              </form>
              <label className="flex cursor-pointer items-center gap-2 text-[11px] text-ink-secondary">
                <Checkbox
                  checked={allFilteredSelected}
                  onCheckedChange={() => toggleSelectAll()}
                  aria-label="Select all deleted issues"
                />
                Select all
              </label>
              <Button
                variant="outline"
                size="sm"
                className="ml-auto"
                disabled={selected.size === 0 || restoring}
                onClick={() =>
                  bulkRestoreMutation.mutate([...selected])
                }
              >
                {bulkRestoreMutation.isPending
                  ? "Restoring…"
                  : `Restore selected${selected.size > 0 ? ` (${selected.size})` : ""}`}
              </Button>
            </div>

            {filtered.length === 0 ? (
              <p className="py-8 text-center text-[12px] text-ink-muted">
                No deleted issues match &ldquo;{search.trim()}&rdquo;.{" "}
                <button
                  type="button"
                  className="underline hover:text-ink"
                  onClick={() => setSearch("")}
                >
                  Clear search
                </button>
              </p>
            ) : (
              <ul className="divide-y divide-border-hairline overflow-hidden rounded-md border border-border">
                {filtered.map((d) => (
                  <li
                    key={d.id}
                    className="flex min-h-[47px] items-center gap-3 px-3 py-2 transition-colors hover:bg-wash-hover"
                  >
                    <Checkbox
                      checked={selected.has(d.id)}
                      onCheckedChange={() => toggleSelect(d.id)}
                      aria-label={`Select ${d.display_id} for bulk restore`}
                      className="shrink-0"
                    />
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2">
                        {/* Plain text, NOT a link: a still-deleted row's
                            detail route would 404. */}
                        <span
                          className="truncate text-[12px] text-ink"
                          title={d.name}
                        >
                          {d.name}
                        </span>
                        <span className="shrink-0 rounded border border-border px-1.5 py-0.5 text-[10px] leading-none text-ink-secondary">
                          Deleted
                        </span>
                      </div>
                      <p className="mt-0.5 text-[10px] text-ink-muted">
                        <span className="font-mono">{d.display_id}</span> ·
                        deleted{" "}
                        {d.deleted_at
                          ? relativeTime(d.deleted_at)
                          : "at an unknown time"}
                      </p>
                    </div>
                    <div className="flex shrink-0 gap-1">
                      <Button
                        variant="outline"
                        size="sm"
                        disabled={restoring}
                        onClick={() => restoreMutation.mutate(d.id)}
                      >
                        Restore
                      </Button>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </>
        )}
      </div>
    </div>
  );
}
