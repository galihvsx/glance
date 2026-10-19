// Project cycles settings (C78T1): the per-project "Parallel cycles"
// toggle. Wording mirrors Plane's Cycles docs; member (15)+ can toggle,
// guests get a read-only view and the server 403s any mutation attempt.
// Grandfathering (C78T0 contract): turning the toggle off does NOT change
// existing overlapping cycles — the server only rejects NEW overlapping
// creates/updates.

import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../../lib/api";
import type { Project } from "../../lib/types";
import { Alert, AlertDescription } from "../ui/alert";
import { Card, CardContent, CardHeader, CardTitle } from "../ui/card";
import { Label } from "../ui/label";
import { Skeleton } from "../ui/skeleton";
import { Switch } from "../ui/switch";

function projectKey(slug: string, identifier: string) {
  return ["project", slug, identifier];
}

function projectPath(slug: string, identifier: string) {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;
}

function errMsg(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message : fallback;
}

export default function CyclesSection({
  slug,
  identifier,
  canEdit,
}: {
  slug: string;
  identifier: string;
  canEdit: boolean;
}) {
  const queryClient = useQueryClient();
  const [enabled, setEnabled] = useState<boolean | null>(null);
  const [error, setError] = useState<string | null>(null);

  const query = useQuery({
    queryKey: projectKey(slug, identifier),
    queryFn: () => api.get<Project>(projectPath(slug, identifier)),
  });

  useEffect(() => {
    if (query.data && enabled === null) {
      setEnabled(query.data.parallel_cycles ?? false);
    }
  }, [query.data, enabled]);

  const toggleMutation = useMutation({
    mutationFn: (next: boolean) =>
      api.patch<Project>(projectPath(slug, identifier), {
        parallel_cycles: next,
      }),
    onMutate: (next) => {
      setEnabled(next);
      setError(null);
    },
    onSuccess: (p) => {
      setEnabled(p.parallel_cycles);
      void queryClient.invalidateQueries({
        queryKey: projectKey(slug, identifier),
      });
    },
    onError: (e) => {
      // Revert the optimistic flip to the last-known server value.
      setEnabled(query.data?.parallel_cycles ?? false);
      setError(errMsg(e, "Failed to update parallel cycles"));
    },
  });

  if (query.isError) {
    return (
      <Alert variant="destructive">
        <AlertDescription>
          {errMsg(query.error, "Failed to load project")}
        </AlertDescription>
      </Alert>
    );
  }
  if (query.isPending || enabled === null) {
    return <Skeleton className="h-48 w-full" />;
  }

  return (
    <Card className="rounded-lg border-border-hairline">
      <CardHeader>
        <CardTitle className="text-xs font-semibold">Cycles</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex items-start justify-between gap-4">
          <div className="space-y-1">
            <Label htmlFor="cycles-parallel">Parallel cycles</Label>
            <p className="text-xs text-muted-foreground">
              Run multiple cycles at the same time with overlapping dates
              (e.g. a two-week sprint alongside a longer release cycle).
              Turned off, only one cycle can be active at a time and cycles
              can&apos;t overlap.
            </p>
          </div>
          <Switch
            id="cycles-parallel"
            aria-label="Parallel cycles"
            checked={enabled}
            disabled={!canEdit || toggleMutation.isPending}
            onCheckedChange={(v) => {
              // The server 403s guest mutations; the guard keeps a
              // read-only switch from ever issuing the PATCH.
              if (canEdit) toggleMutation.mutate(v);
            }}
          />
        </div>
        <p className="text-xs text-muted-foreground">
          Turning this off doesn&apos;t change existing overlapping cycles —
          new overlapping cycles will be rejected.
        </p>
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
      </CardContent>
    </Card>
  );
}
