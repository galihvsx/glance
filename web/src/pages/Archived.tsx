// Archived view (C7T5): archived issues (archived_at set) for the project.
// Route: /w/:slug/p/:identifier/archived (inside ProjectScope).
//
// Actions per archived issue: Unarchive (PATCH {archived: false}), Delete
// (with confirm; backend DELETE is a soft delete, consistent with every
// other delete in the app — there is no hard-delete endpoint).
// "Archived by" is not shown: the backend has no archived_by field, only
// the archived date.

import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../lib/api";
import {
  archivedKey,
  deleteArchivedIssue,
  fetchArchived,
  isArchived,
  unarchiveIssue,
} from "../lib/archived";
import type { IssueListResult } from "../lib/types";
import { relativeTime } from "../lib/relativeTime";
import PageHeader from "../components/shell/PageHeader";
import PqlEditor from "../components/issue/PqlEditor";
import EmptyState from "../components/personal/EmptyState";
import { Alert, AlertDescription } from "../components/ui/alert";
import { Button } from "../components/ui/button";
import { Skeleton } from "../components/ui/skeleton";
import { Archive } from "lucide-react";

function errMsg(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message : fallback;
}

export default function Archived() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const queryClient = useQueryClient();

  const archivedQuery = useQuery({
    queryKey: archivedKey(slug, identifier),
    queryFn: () => fetchArchived(slug, identifier),
    enabled: slug !== "" && identifier !== "",
  });
  // C111T1: PQL mode on the archived list, copying the Cycles-detail
  // shape verbatim (local appliedQuery state, probe-first apply, no
  // URL writes, no default auto-apply). CRITICAL SEMANTIC:
  // `archived=1` only INCLUDES archived rows — it drops the default
  // working-set filter but adds no "archived only" restriction on the
  // backend (see lib/archived.ts). The client-side
  // `archived_at != null` filter below is what makes this view
  // archived-only, exactly as the unfiltered fetch does today, so
  // working-set matches of a PQL predicate never leak into the view.
  // The PQL-mode list is a single page (per_page=100), matching the
  // unfiltered archived fetch — no cursor loop, unlike Cycles.
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  const [pqlMode, setPqlMode] = useState(false);
  const [appliedQuery, setAppliedQuery] = useState("");
  const pqlArchivedQuery = useQuery({
    queryKey: ["archived-pql", slug, identifier, appliedQuery],
    enabled: pqlMode && appliedQuery.trim() !== "",
    queryFn: () =>
      api
        .get<IssueListResult>(
          `${base}/issues?${new URLSearchParams({
            archived: "1",
            pql: appliedQuery.trim(),
            per_page: "100",
          }).toString()}`,
        )
        .then((r) => (r.results ?? []).filter(isArchived)),
  });
  // Probe-first apply (Cycles-detail pattern): fetch one page with
  // archived=1 + pql and commit only on success. A 400 rejects, so
  // PqlEditor shows it inline and the displayed list is untouched
  // (appliedQuery — and therefore the displayed list — never changes).
  // Empty query = no filter: stays in PQL mode, shows the full list.
  async function handlePqlApply(query: string): Promise<void> {
    const q = query.trim();
    if (!q) {
      setAppliedQuery("");
      return;
    }
    const p = new URLSearchParams({
      archived: "1",
      pql: q,
      per_page: "100",
    });
    await api.get<IssueListResult>(`${base}/issues?${p.toString()}`);
    setAppliedQuery(q);
  }
  function handlePqlModeChange(mode: "all" | "pql") {
    if (mode === "pql") {
      setPqlMode(true);
      return;
    }
    // Leaving PQL mode clears the applied query, so the unfiltered
    // archived list (already cached) is restored immediately.
    setPqlMode(false);
    setAppliedQuery("");
  }
  // While a committed PQL query is still loading, keep showing the
  // unfiltered archived list rather than blanking it — the filtered
  // list replaces it as soon as it arrives.
  const archived = archivedQuery.data ?? [];
  const displayed =
    pqlMode && appliedQuery.trim() !== ""
      ? (pqlArchivedQuery.data ?? archived)
      : archived;
  const invalidate = () => {
    void queryClient.invalidateQueries({
      queryKey: archivedKey(slug, identifier),
    });
    // The PQL-mode list is a separate query key — prefix-invalidate
    // it too, so a PQL-filtered list re-runs in place after a
    // mutation (same shape as the unfiltered list above). Harmless
    // when PQL mode is inactive: that query is disabled and only
    // refetches on next enable.
    void queryClient.invalidateQueries({
      queryKey: ["archived-pql", slug, identifier],
    });
    void queryClient.invalidateQueries({
      queryKey: ["issues", slug, identifier],
    });
  };

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteArchivedIssue(slug, identifier, id),
    onSuccess: () => invalidate(),
  });

  const unarchiveMutation = useMutation({
    mutationFn: (id: string) => unarchiveIssue(slug, identifier, id),
    onSuccess: () => invalidate(),
  });

  const error =
    deleteMutation.error ?? unarchiveMutation.error ?? archivedQuery.error;

  return (
    <div className="w-full">
      {/* C19T4: shared page header replaces the per-page h1 + ProjectNav. */}
      <PageHeader
        viewName="Archived"
        count={displayed.length}
      />
      <div className="mx-auto w-full max-w-5xl space-y-4 p-4">
        {/* C111T1: All-archived / PQL toggle + editor, above the list
            (Cycles-detail toggle pattern). No SavedViewsMenu here:
            there is no Views save path on this page yet — wave 2
            wires the menu and flips the editor's save hint. */}
        <div className="flex items-center gap-2">
          <div
            role="group"
            aria-label="Issue list mode"
            className="flex flex-1 rounded-md border p-0.5"
          >
            <button
              type="button"
              aria-pressed={!pqlMode}
              onClick={() => handlePqlModeChange("all")}
              className={`h-7 flex-1 rounded-sm text-xs font-medium ${
                !pqlMode ? "bg-primary text-primary-foreground" : ""
              }`}
            >
              All archived
            </button>
            <button
              type="button"
              aria-pressed={pqlMode}
              onClick={() => handlePqlModeChange("pql")}
              className={`h-7 flex-1 rounded-sm text-xs font-medium ${
                pqlMode ? "bg-primary text-primary-foreground" : ""
              }`}
            >
              PQL
            </button>
          </div>
        </div>
        {pqlMode && (
          // Keyed by the applied query: PqlEditor's draft is
          // initial-value-only, so the editor re-seeds on re-apply.
          <PqlEditor
            key={appliedQuery}
            appliedQuery={appliedQuery}
            onApply={handlePqlApply}
            onClear={() => {
              setAppliedQuery("");
            }}
            showSaveHint={false}
            // No Views save path on this page yet (wave 2 wires the
            // menu and flips showSaveHint to true with the corrected
            // C108T1 hint text). The text is staged here so the flip
            // is a boolean change, not a wording decision.
            saveHintText="There is no Views save path for this query on this page yet."
          />
        )}

        {error && (
          <Alert variant="destructive">
            <AlertDescription>
              {errMsg(error, "Something went wrong")}
            </AlertDescription>
          </Alert>
        )}

        {archivedQuery.isLoading ? (
          <div className="space-y-2">
            <Skeleton className="h-[47px] w-full" />
            <Skeleton className="h-[47px] w-full" />
          </div>
        ) : displayed.length === 0 ? (
          <EmptyState
            icon={Archive}
            title={
              pqlMode &&
              appliedQuery.trim() !== "" &&
              pqlArchivedQuery.data !== undefined
                ? "No issues match this PQL query"
                : "No archived issues"
            }
          >
            {/* A valid query that matches nothing is a real empty
                result, not "no archived issues" — say so, so the
                empty state never masquerades as an empty archive. */}
            {pqlMode &&
            appliedQuery.trim() !== "" &&
            pqlArchivedQuery.data !== undefined ? null : (
              <p>
                Issues archived out of the working set are parked here.
              </p>
            )}
          </EmptyState>
        ) : (
          <ul className="divide-y divide-border-hairline overflow-hidden rounded-md border border-border">
            {displayed.map((a) => (
              <li
                key={a.id}
                className="flex min-h-[47px] items-center gap-3 px-3 py-2 transition-colors hover:bg-wash-hover"
              >
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <Link
                      to={`/w/${slug}/p/${identifier}/i/${a.id}`}
                      className="truncate text-[12px] text-ink hover:underline"
                    >
                      {a.name}
                    </Link>
                    <span className="shrink-0 rounded border border-border px-1.5 py-0.5 text-[10px] leading-none text-ink-secondary">
                      Archived
                    </span>
                  </div>
                  <p className="mt-0.5 text-[10px] text-ink-muted">
                    <span className="font-mono">{a.display_id}</span> · archived{" "}
                    {a.archived_at ? relativeTime(a.archived_at) : "at an unknown time"}
                  </p>
                </div>
                <div className="flex shrink-0 gap-1">
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={unarchiveMutation.isPending}
                    onClick={() => unarchiveMutation.mutate(a.id)}
                  >
                    Unarchive
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={deleteMutation.isPending}
                    onClick={() => {
                      if (
                        window.confirm(
                          `Delete archived issue "${a.name}"? This can't be undone.`,
                        )
                      )
                        deleteMutation.mutate(a.id);
                    }}
                  >
                    Delete
                  </Button>
                </div>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
