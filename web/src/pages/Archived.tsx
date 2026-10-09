// Archived view (C7T5): archived issues (archived_at set) for the project.
// Route: /w/:slug/p/:identifier/archived (inside ProjectScope).
//
// Actions per archived issue: Unarchive (disabled — the backend has no
// archive/unarchive endpoint, verified 2026-10-09; the button carries an
// explanatory tooltip instead of pretending to work), Delete (with confirm;
// backend DELETE is a soft delete, consistent with every other delete in the
// app — there is no hard-delete endpoint). "Archived by" is not shown: the
// backend has no archived_by field, only the archived date.

import { Link, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../lib/api";
import {
  archivedKey,
  deleteArchivedIssue,
  fetchArchived,
} from "../lib/archived";
import { relativeTime } from "../lib/relativeTime";
import ProjectNav from "../components/project/ProjectNav";
import NotificationBell from "../components/notifications/NotificationBell";
import ThemeToggle from "../components/ThemeToggle";
import { Alert, AlertDescription } from "../components/ui/alert";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Card, CardContent } from "../components/ui/card";
import { Skeleton } from "../components/ui/skeleton";

const UNARCHIVE_UNAVAILABLE =
  "Unarchiving isn't available yet — the backend has no archive/unarchive endpoint (flagged for the coordinator).";

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
  const invalidate = () => {
    void queryClient.invalidateQueries({
      queryKey: archivedKey(slug, identifier),
    });
    void queryClient.invalidateQueries({
      queryKey: ["issues", slug, identifier],
    });
  };

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteArchivedIssue(slug, identifier, id),
    onSuccess: () => invalidate(),
  });

  const error = deleteMutation.error ?? archivedQuery.error;

  const archived = archivedQuery.data ?? [];

  return (
    <div className="mx-auto max-w-5xl space-y-4 p-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">Archived</h1>
        <div className="flex items-center gap-2">
          <NotificationBell />
          <ThemeToggle />
        </div>
      </div>
      <ProjectNav />

      {error && (
        <Alert variant="destructive">
          <AlertDescription>
            {errMsg(error, "Something went wrong")}
          </AlertDescription>
        </Alert>
      )}

      {archivedQuery.isLoading ? (
        <Skeleton className="h-32 w-full" />
      ) : archived.length === 0 ? (
        <Card>
          <CardContent className="p-6 text-sm text-muted-foreground">
            No archived issues. Issues archived out of the working set are
            parked here.
          </CardContent>
        </Card>
      ) : (
        <ul className="divide-y rounded-md border">
          {archived.map((a) => (
            <li key={a.id} className="flex items-center gap-3 px-3 py-2.5">
              <div className="flex-1">
                <div className="flex items-center gap-2">
                  <Link
                    to={`/w/${slug}/p/${identifier}/i/${a.id}`}
                    className="text-sm font-medium hover:underline"
                  >
                    {a.name}
                  </Link>
                  <Badge variant="secondary">Archived</Badge>
                </div>
                <p className="text-xs text-muted-foreground">
                  <span className="font-mono">{a.display_id}</span> · archived{" "}
                  {a.archived_at ? relativeTime(a.archived_at) : "at an unknown time"}
                </p>
              </div>
              <div className="flex shrink-0 gap-1">
                <span title={UNARCHIVE_UNAVAILABLE}>
                  <Button variant="outline" size="sm" disabled>
                    Unarchive
                  </Button>
                </span>
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
  );
}
