// Project visibility toggle (C92T1, prototype-as-spec): lives in the
// General tab of Project Settings. Public = today's behavior (every
// workspace member can access the project). Private = only project
// members and members of the linked teamspace can access it — the
// project vanishes from other members' sidebar, project list, and
// search, and its direct URL shows the same not-found state as a
// missing project. Changing visibility requires effective project
// Admin (20) — workspace Admin, or a direct Admin row for the caller
// (resolved from the members roster + currentUserId). Non-admins see
// the current visibility read-only, never a toggle. The server is
// the gate: a failed save leaves the toggle on the old value with an
// honest error, never an optimistic lie. Existing projects are
// Public (backfilled) — nothing changes until an admin flips one.

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../../lib/api";
import type { Project } from "../../lib/types";
import {
  fetchProjectMembers,
  projectMemberKeys,
} from "../../lib/projectMembers";
import { Alert, AlertDescription } from "../ui/alert";
import { Button } from "../ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../ui/card";
import { Skeleton } from "../ui/skeleton";

function errMsg(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message : fallback;
}

export default function VisibilitySection({
  slug,
  identifier,
  isWorkspaceAdmin,
  currentUserId,
}: {
  slug: string;
  identifier: string;
  /** Workspace Admin (role 20) — always an effective project Admin. */
  isWorkspaceAdmin: boolean;
  /** The caller's user id, for direct-Admin detection. Undefined
   *  when unknown: only the workspace-Admin signal gates the toggle. */
  currentUserId?: string | null;
}) {
  const queryClient = useQueryClient();
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  const projectQuery = useQuery({
    queryKey: ["project", slug, identifier],
    queryFn: () =>
      api.get<Project>(
        `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`,
      ),
  });
  const rosterQuery = useQuery({
    queryKey: projectMemberKeys(slug, identifier).list,
    queryFn: () => fetchProjectMembers(slug, identifier),
  });

  const savedVisibility = projectQuery.data?.visibility ?? "public";
  const canManage =
    isWorkspaceAdmin ||
    (currentUserId != null &&
      (rosterQuery.data ?? []).some(
        (m) => m.user_id === currentUserId && m.role === 20,
      ));

  const saveMutation = useMutation({
    mutationFn: (visibility: string) =>
      api.patch<Project>(
        `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`,
        { visibility },
      ),
    onSuccess: (p) => {
      setError(null);
      setSaved(true);
      queryClient.setQueryData(["project", slug, identifier], p);
      void queryClient.invalidateQueries({
        queryKey: ["project", slug, identifier],
      });
      void queryClient.invalidateQueries({ queryKey: ["projects", slug] });
    },
    onError: (e) => {
      setError(errMsg(e, "Failed to change visibility"));
      setSaved(false);
    },
  });

  if (projectQuery.isPending) {
    return <Skeleton className="h-32 w-full" />;
  }
  if (projectQuery.isError) {
    return (
      <Alert variant="destructive">
        <AlertDescription>
          {errMsg(projectQuery.error, "Failed to load project")}
        </AlertDescription>
      </Alert>
    );
  }

  const isPrivate = savedVisibility === "private";

  return (
    <Card className="rounded-lg border-border-hairline">
      <CardHeader>
        <CardTitle className="text-xs font-semibold">Visibility</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {canManage ? (
          <div className="flex gap-2" role="group" aria-label="Visibility">
            <Button
              variant={isPrivate ? "outline" : "default"}

              size="sm"
              disabled={saveMutation.isPending}
              onClick={() => {
                setSaved(false);
                saveMutation.mutate("public");
              }}
            >
              Public
            </Button>
            <Button
              variant={isPrivate ? "default" : "outline"}
              size="sm"
              disabled={saveMutation.isPending}
              onClick={() => {
                setSaved(false);
                saveMutation.mutate("private");
              }}
            >
              Private
            </Button>
          </div>
        ) : (
          <p className="text-[13px]">
            {isPrivate ? "Private" : "Public"}
            <span className="text-ink-secondary">
              {" "}
              — only project admins can change visibility.
            </span>
          </p>
        )}
        <p className="text-[12px] text-ink-secondary">
          Public: every workspace member can access this project
          (today&apos;s behavior). Private: only project members and
          members of the linked teamspace can access it — the project
          vanishes from other members&apos; sidebar, project list, and
          search, and its direct URL shows the same not-found state as
          a missing project. Changing visibility requires project
          Admin or workspace Admin.
        </p>
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        {saved && !saveMutation.isPending && (
          <p className="text-xs text-muted-foreground">Saved.</p>
        )}
      </CardContent>
    </Card>
  );
}
