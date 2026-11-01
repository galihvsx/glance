// Drafts view (C6T4): unfinished issues (is_draft=true) for the project.
// Route: /w/:slug/p/:identifier/drafts (inside ProjectScope).
//
// Actions per draft: Resume (opens the issue detail for editing),
// Publish (is_draft=false → joins the working set), Discard (delete with
// confirm). Drafts never appear in list/board/calendar views — the list
// endpoint excludes them by default.

import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, api } from "../lib/api";
import {
  discardDraft,
  draftsKey,
  fetchDrafts,
  publishDraft,
} from "../lib/drafts";
import type { IssueListResult } from "../lib/types";
import PqlEditor from "../components/issue/PqlEditor";
import SavedViewsMenu from "../components/issue/SavedViewsMenu";
import { EMPTY_FILTERS } from "../lib/filters";
import { formatPqlError } from "../lib/pql";
import { relativeTime } from "../lib/relativeTime";
import PageHeader from "../components/shell/PageHeader";
import EmptyState from "../components/personal/EmptyState";
import { Alert, AlertDescription } from "../components/ui/alert";
import { Button, buttonVariants } from "../components/ui/button";
import { Skeleton } from "../components/ui/skeleton";
import { FilePenLine } from "lucide-react";

function errMsg(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message : fallback;
}

export default function Drafts() {
  const { slug = "", identifier = "" } = useParams<{
    slug: string;
    identifier: string;
  }>();
  const queryClient = useQueryClient();

  const draftsQuery = useQuery({
    queryKey: draftsKey(slug, identifier),
    queryFn: () => fetchDrafts(slug, identifier),
    enabled: slug !== "" && identifier !== "",
  });

  // C111T2: PQL mode on the drafts list, copying the Cycles-detail
  // shape verbatim (C95T1/C107T1): local appliedQuery state, no URL
  // writes, no default auto-apply, probe-first handlePqlApply
  // through ?draft=true&pql=...&per_page=100. A 400 rejects in the
  // probe, so PqlEditor shows it inline and the displayed set is
  // untouched (appliedQuery — and therefore displayedDrafts — never
  // changes). Empty query = no filter: stays in PQL mode, shows
  // the full draft set. Leaving PQL mode clears the applied query
  // and restores the unfiltered draft set.
  // NO client-side filter is needed here, and none must be added:
  // draft mode selects is_draft = TRUE server-side, so a PQL query
  // on Drafts never leaks published issues into the view (contrast
  // Archived, where archived=1 only includes and the page keeps a
  // client-side archived_at != null filter). C111T4: the detail-mode
  // SavedViewsMenu below is now the Views save path, so the editor
  // passes showSaveHint={true} with the corrected C108T1 hint text.
  // A view apply never shows published issues — draft mode is the
  // scope, same as a typed query: handleSavedViewApplied probes
  // through handlePqlApply, which already carries draft=true, so
  // the server-side is_draft selection applies to view applies
  // exactly as to typed queries.
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  const [pqlMode, setPqlMode] = useState(false);
  const [appliedQuery, setAppliedQuery] = useState("");
  // C111T4: same ["project", slug, identifier] query Issues/Board
  // use — the menu needs a project UUID, and the shared key is
  // cache-warm whenever the user came from another project page.
  const projectQuery = useQuery({
    queryKey: ["project", slug, identifier],
    queryFn: () => api.get<{ id: string }>(base),
  });
  // C111T4: a saved view whose query fails its probe has no editor
  // inline error to land in — the editor only reports failures from
  // its own Apply button, and it mounts fresh (empty draft) for a
  // menu apply. The failure is surfaced here, at the detail level,
  // instead of vanishing or rejecting unhandled.
  const [savedViewError, setSavedViewError] = useState<string | null>(null);
  const pqlDraftsQuery = useQuery({
    queryKey: ["drafts-pql", slug, identifier, appliedQuery],
    enabled:
      pqlMode && appliedQuery.trim() !== "" && slug !== "" && identifier !== "",
    queryFn: async () => {
      const p = new URLSearchParams({
        draft: "true",
        pql: appliedQuery.trim(),
        per_page: "100",
      });
      const page = await api.get<IssueListResult>(
        `${base}/issues?${p.toString()}`,
      );
      return page.results ?? [];
    },
  });
  async function handlePqlApply(query: string): Promise<void> {
    const q = query.trim();
    if (!q) {
      setAppliedQuery("");
      return;
    }
    const p = new URLSearchParams({
      draft: "true",
      pql: q,
      per_page: "100",
    });
    await api.get<IssueListResult>(`${base}/issues?${p.toString()}`);
    setAppliedQuery(q);
    setSavedViewError(null);
  }
  function handlePqlModeChange(mode: "all" | "pql") {
    if (mode === "pql") {
      setPqlMode(true);
      return;
    }
    // Leaving PQL mode clears the applied query, so the unfiltered
    // draft list (already cached) is restored immediately.
    setPqlMode(false);
    setAppliedQuery("");
    setSavedViewError(null);
  }
  // C111T4: applying a saved PQL view from the detail-mode menu,
  // copying the Cycles shape verbatim. Probe FIRST, then enter PQL
  // mode: the probe commits appliedQuery while still in all-drafts
  // mode (displayedDrafts is unaffected until pqlMode flips), so the
  // editor mounts seeded with the applied query. A 400 leaves
  // appliedQuery untouched — the list never changes — and the error
  // surfaces in the detail-level alert (the editor cannot show it:
  // it mounts fresh with an empty draft). The menu ignores this
  // handler's return value, so the catch here is load-bearing:
  // without it the rejected probe would be an unhandled rejection.
  // Null is unreachable from the detail-mode menu (PQL views only)
  // and mirrors Cycles: leave PQL mode. No URL writes, no default
  // auto-apply, PQL views only.
  async function handleSavedViewApplied(pqlQuery: string | null) {
    if (pqlQuery === null) {
      setPqlMode(false);
      setAppliedQuery("");
      setSavedViewError(null);
      return;
    }
    try {
      await handlePqlApply(pqlQuery);
    } catch (e) {
      setSavedViewError(formatPqlError(e));
    }
    setPqlMode(true);
  }
  // While a committed PQL query is still loading, keep showing the
  // unfiltered list rather than blanking it — the filtered list
  // replaces it as soon as it arrives.
  const displayedDrafts =
    pqlMode && appliedQuery.trim() !== ""
      ? (pqlDraftsQuery.data ?? draftsQuery.data ?? [])
      : (draftsQuery.data ?? []);

  const invalidate = () => {
    void queryClient.invalidateQueries({
      queryKey: draftsKey(slug, identifier),
    });
    // Publish / Discard from a PQL-filtered list re-runs the list
    // in place: the PQL-mode query is invalidated alongside the
    // unfiltered one (prefix key covers every applied query).
    void queryClient.invalidateQueries({
      queryKey: ["drafts-pql", slug, identifier],
    });
  };

  const publishMutation = useMutation({
    mutationFn: (id: string) => publishDraft(slug, identifier, id),
    onSuccess: () => {
      void invalidate();
      // The published issue joins the working-set lists.
      void queryClient.invalidateQueries({
        queryKey: ["issues", slug, identifier],
      });
    },
  });

  const discardMutation = useMutation({
    mutationFn: (id: string) => discardDraft(slug, identifier, id),
    onSuccess: () => void invalidate(),
  });

  const error =
    publishMutation.error ?? discardMutation.error ?? draftsQuery.error;

  const drafts = displayedDrafts;

  return (
    <div className="w-full">
      {/* C19T4: shared page header replaces the per-page h1 + ProjectNav. */}
      <PageHeader
        viewName="Drafts"
        count={drafts.length}
      />
      <div className="mx-auto w-full max-w-5xl space-y-4 p-4">
        {/* C111T2: All-drafts / PQL toggle + editor, above the draft
            list (Cycles detail pattern verbatim). PQL mode changes
            ONLY the displayed list; Publish / Discard work on the
            displayed rows and re-run the list in place. C111T4:
            detail toolbar — the toggle plus the detail-mode
            SavedViewsMenu beside it. Detail mode guarantees no URL
            writes and no default auto-apply, so the page settles
            PQL state through onViewApplied only. */}
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
              All drafts
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
          <SavedViewsMenu
            projectId={projectQuery.data?.id ?? ""}
            filters={EMPTY_FILTERS}
            display={null}
            onApplyDisplay={null}
            pqlMode={pqlMode}
            pqlQuery={appliedQuery}
            onViewApplied={handleSavedViewApplied}
            detailMode
          />
        </div>
        {savedViewError && (
          <Alert variant="destructive">
            <AlertDescription>{savedViewError}</AlertDescription>
          </Alert>
        )}
        {pqlMode && (
          // Keyed by the applied query: PqlEditor's draft is
          // initial-value-only, so without this a re-apply would
          // leave the editor showing the previous draft instead of
          // the applied query (C107 lesson).
          <PqlEditor
            key={appliedQuery}
            appliedQuery={appliedQuery}
            onApply={handlePqlApply}
            onClear={() => {
              setAppliedQuery("");
            }}
            showSaveHint={true}
            saveHintText="To save this query, use Views → Save current view next to the All drafts / PQL toggle; it re-applies when the view is opened."
          />
        )}

        {error && (
          <Alert variant="destructive">
            <AlertDescription>
              {errMsg(error, "Something went wrong")}
            </AlertDescription>
          </Alert>
        )}

        {draftsQuery.isLoading ? (
          <div className="space-y-2">
            <Skeleton className="h-[47px] w-full" />
            <Skeleton className="h-[47px] w-full" />
          </div>
        ) : drafts.length === 0 ? (
          <EmptyState
            icon={FilePenLine}
            title={
              pqlMode &&
              appliedQuery.trim() !== "" &&
              pqlDraftsQuery.data !== undefined
                ? "No drafts match this PQL query"
                : "No drafts"
            }
          >
            {pqlMode &&
            appliedQuery.trim() !== "" &&
            pqlDraftsQuery.data !== undefined ? (
              <p>
                Try a different query, or clear it to see all drafts.
              </p>
            ) : (
              <p>
                Start a new issue and choose &ldquo;Save as draft&rdquo; to
                park an unfinished idea here.
              </p>
            )}
          </EmptyState>
        ) : (
          <ul className="divide-y divide-border-hairline overflow-hidden rounded-md border border-border">
            {drafts.map((d) => (
              <li
                key={d.id}
                className="flex min-h-[47px] items-center gap-3 px-3 py-2 transition-colors hover:bg-wash-hover"
              >
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <Link
                      to={`/w/${slug}/p/${identifier}/i/${d.id}`}
                      className="truncate text-[12px] text-ink hover:underline"
                    >
                      {d.name}
                    </Link>
                    <span className="shrink-0 rounded border border-border px-1.5 py-0.5 text-[10px] leading-none text-ink-secondary">
                      Draft
                    </span>
                  </div>
                  <p className="mt-0.5 text-[10px] text-ink-muted">
                    <span className="font-mono">{d.display_id}</span> · updated{" "}
                    {relativeTime(d.updated_at)}
                  </p>
                </div>
                <div className="flex shrink-0 gap-1">
                  <Link
                    to={`/w/${slug}/p/${identifier}/i/${d.id}`}
                    className={buttonVariants({ variant: "ghost", size: "sm" })}
                  >
                    Resume
                  </Link>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={publishMutation.isPending}
                    onClick={() => publishMutation.mutate(d.id)}
                  >
                    Publish
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={discardMutation.isPending}
                    onClick={() => {
                      if (
                        window.confirm(
                          `Discard draft "${d.name}"? This can't be undone.`,
                        )
                      )
                        discardMutation.mutate(d.id);
                    }}
                  >
                    Discard
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
