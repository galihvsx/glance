// Archived view (C7T5): archived issues (archived_at set) for the project.
// Route: /w/:slug/p/:identifier/archived (inside ProjectScope).
//
// Actions per archived issue: Unarchive (PATCH {archived: false}), Delete
// (with confirm; backend DELETE is a soft delete, consistent with every
// other delete in the app — there is no hard-delete endpoint).
// "Archived by" is not shown: the backend has no archived_by field, only
// the archived date.

import { Link, useParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../lib/api";
import {
  archivedKey,
  deleteArchivedIssue,
  fetchArchived,
  unarchiveIssue,
} from "../lib/archived";
import { relativeTime } from "../lib/relativeTime";
import PageHeader from "../components/shell/PageHeader";
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

  const unarchiveMutation = useMutation({
    mutationFn: (id: string) => unarchiveIssue(slug, identifier, id),
    onSuccess: () => invalidate(),
  });

  const error =
    deleteMutation.error ?? unarchiveMutation.error ?? archivedQuery.error;

  const archived = archivedQuery.data ?? [];

  return (
    <div className="w-full">
      {/* C19T4: shared page header replaces the per-page h1 + ProjectNav. */}
      <PageHeader
        viewName="Archived"
        count={archived.length}
      />
      <div className="mx-auto w-full max-w-5xl space-y-4 p-4">

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
        ) : archived.length === 0 ? (
          <EmptyState icon={Archive} title="No archived issues">
            <p>
              Issues archived out of the working set are parked here.
            </p>
          </EmptyState>
        ) : (
          <ul className="divide-y divide-border-hairline overflow-hidden rounded-md border border-border">
            {archived.map((a) => (
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
