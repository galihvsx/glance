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
  // client-side archived_at != null filter). No Views save path on
  // this page yet (Board precedent), so the editor passes
  // showSaveHint={false}; wave 2 wires the detail-mode menu and
  // flips it — do not add the menu here.
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
  const [pqlMode, setPqlMode] = useState(false);
  const [appliedQuery, setAppliedQuery] = useState("");
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
            displayed rows and re-run the list in place. */}
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
        </div>
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
            showSaveHint={false}
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
