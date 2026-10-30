// Project teamspace picker (C89T1): the move control lives on the
// Project settings page, not on the sidebar row or the Projects card.
// A dropdown of the workspace's teamspaces + "Ungrouped", saving via
// the T0 PATCH (teamspace_id UUID or explicit null). The picker shows
// the current assignment; a failed save reverts to the old value with
// an honest error — no optimistic lie (the select is disabled while
// the save is in flight, and onError restores the saved value).

import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../../lib/api";
import type { Project } from "../../lib/types";
import {
  fetchTeamspaces,
  moveProjectToTeamspace,
  teamspaceKeys,
} from "../../lib/teamspaces";
import { Alert, AlertDescription } from "../ui/alert";
import { Card, CardContent, CardHeader, CardTitle } from "../ui/card";
import { Label } from "../ui/label";
import { Skeleton } from "../ui/skeleton";

function errMsg(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message : fallback;
}

export default function TeamspaceSection({
  slug,
  identifier,
  canEdit,
}: {
  slug: string;
  identifier: string;
  canEdit: boolean;
}) {
  const queryClient = useQueryClient();
  const projectQuery = useQuery({
    queryKey: ["project", slug, identifier],
    queryFn: () =>
      api.get<Project>(
        `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`,
      ),
  });
  const teamspacesQuery = useQuery({
    queryKey: teamspaceKeys(slug).list,
    queryFn: () => fetchTeamspaces(slug),
  });

  const savedValue = projectQuery.data?.teamspace_id ?? "";
  // Draft selection while a save is in flight; null = show saved value.
  const [draft, setDraft] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  // A fresh project payload (after a successful save + refetch, or a
  // navigation) clears any stale draft.
  useEffect(() => {
    setDraft(null);
  }, [projectQuery.data?.teamspace_id]);

  const saveMutation = useMutation({
    mutationFn: (teamspaceId: string | null) =>
      moveProjectToTeamspace(slug, identifier, teamspaceId),
    onSuccess: () => {
      setError(null);
      setDraft(null);
      void queryClient.invalidateQueries({
        queryKey: ["project", slug, identifier],
      });
      void queryClient.invalidateQueries({ queryKey: ["projects", slug] });
      void queryClient.invalidateQueries({
        queryKey: teamspaceKeys(slug).list,
      });
    },
    onError: (e) => {
      // Revert to the old value — the failed save must not stick.
      setDraft(null);
      setError(errMsg(e, "Failed to move project"));
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

  const value = draft ?? savedValue;
  const teamspaces = teamspacesQuery.data ?? [];

  return (
    <Card className="rounded-lg border-border-hairline">
      <CardHeader>
        <CardTitle className="text-xs font-semibold">Teamspace</CardTitle>
      </CardHeader>
      <CardContent className="space-y-2">
        <Label htmlFor="teamspace-picker">
          Group this project under a teamspace in the sidebar.
        </Label>
        <select
          id="teamspace-picker"
          aria-label="Teamspace"
          className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs outline-none disabled:cursor-not-allowed disabled:opacity-50"
          value={value}
          disabled={!canEdit || saveMutation.isPending}
          onChange={(e) => {
            const next = e.target.value;
            setDraft(next);
            setError(null);
            saveMutation.mutate(next === "" ? null : next);
          }}
        >
          <option value="">Ungrouped</option>
          {teamspaces.map((t) => (
            <option key={t.id} value={t.id}>
              {t.name}
            </option>
          ))}
        </select>
        {saveMutation.isPending && (
          <p className="text-xs text-muted-foreground">Saving…</p>
        )}
        {!saveMutation.isPending && draft === null && projectQuery.data && (
          <p className="text-xs text-muted-foreground">
            {savedValue
              ? `Currently in ${teamspaces.find((t) => t.id === savedValue)?.name ?? "a teamspace"}.`
              : "Currently ungrouped."}
          </p>
        )}
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
      </CardContent>
    </Card>
  );
}
