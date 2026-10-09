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
import ProjectNav from "../components/project/ProjectNav";
import { Alert, AlertDescription } from "../components/ui/alert";
import { Badge } from "../components/ui/badge";
import { Button, buttonVariants } from "../components/ui/button";
import { Card, CardContent } from "../components/ui/card";
import { Skeleton } from "../components/ui/skeleton";

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
    <div className="mx-auto max-w-5xl space-y-4 p-4">
      <h1 className="text-xl font-semibold">Drafts</h1>
      <ProjectNav />

      {error && (
        <Alert variant="destructive">
          <AlertDescription>
            {errMsg(error, "Something went wrong")}
          </AlertDescription>
        </Alert>
      )}

      {draftsQuery.isLoading ? (
        <Skeleton className="h-32 w-full" />
      ) : drafts.length === 0 ? (
        <Card>
          <CardContent className="p-6 text-sm text-muted-foreground">
            No drafts. Start a new issue and choose “Save as draft” to park
            an unfinished idea here.
          </CardContent>
        </Card>
      ) : (
        <ul className="divide-y rounded-md border">
          {drafts.map((d) => (
            <li key={d.id} className="flex items-center gap-3 px-3 py-2.5">
              <div className="flex-1">
                <div className="flex items-center gap-2">
                  <Link
                    to={`/w/${slug}/p/${identifier}/i/${d.id}`}
                    className="text-sm font-medium hover:underline"
                  >
                    {d.name}
                  </Link>
                  <Badge variant="secondary">Draft</Badge>
                </div>
                <p className="text-xs text-muted-foreground">
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
  );
}
