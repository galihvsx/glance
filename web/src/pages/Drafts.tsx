// Drafts view (C6T4): unfinished issues (is_draft=true) for the project.
// Route: /w/:slug/p/:identifier/drafts (inside ProjectScope).
//
// Actions per draft: Resume (opens the issue detail for editing),
// Publish (is_draft=false → joins the working set), Discard (delete with
// confirm). Drafts never appear in list/board/calendar views — the list
// endpoint excludes them by default.

import { Link, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../lib/api";
import {
  discardDraft,
  draftsKey,
  fetchDrafts,
  publishDraft,
} from "../lib/drafts";
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
  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: draftsKey(slug, identifier) });

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

  const drafts = draftsQuery.data ?? [];

  return (
    <div className="w-full">
      {/* C19T4: shared page header replaces the per-page h1 + ProjectNav. */}
      <PageHeader
        viewName="Drafts"
        count={drafts.length}
      />
      <div className="mx-auto w-full max-w-5xl space-y-4 p-4">

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
          <EmptyState icon={FilePenLine} title="No drafts">
            <p>
              Start a new issue and choose &ldquo;Save as draft&rdquo; to park
              an unfinished idea here.
            </p>
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
