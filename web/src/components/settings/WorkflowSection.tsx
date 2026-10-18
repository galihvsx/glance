// Workflow transition controls (C68T1): per-project state-transition
// allowlist editor. Backend contract (C68T0):
//   GET/PUT /api/v1/workspaces/{slug}/projects/{identifier}/workflow
//     → {states, transitions: [{from_state_id, to_state_id}]}
// A project with zero transition rows is unrestricted (any state move is
// allowed); saving rules restricts movement. The server is the gate
// (member 15+ for PUT); the UI mirrors the 400/403 errors inline.

import { useEffect, useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../../lib/api";
import {
  setWorkflow,
  useWorkflow,
  type WorkflowTransition,
} from "../../lib/workflow";
import { Alert, AlertDescription } from "../ui/alert";
import { Button } from "../ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../ui/card";
import { Skeleton } from "../ui/skeleton";
import StateDot from "../issue/StateDot";

function errMsg(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message : fallback;
}

export default function WorkflowSection({
  slug,
  identifier,
  canEdit,
}: {
  slug: string;
  identifier: string;
  canEdit: boolean;
}) {
  const queryClient = useQueryClient();
  const workflowQuery = useWorkflow(slug, identifier);
  const [draft, setDraft] = useState<Map<string, Set<string>> | null>(null);
  const [error, setError] = useState<string | null>(null);

  // Initialize/reset the draft whenever the server data changes.
  useEffect(() => {
    const data = workflowQuery.data;
    if (!data) return;
    const map = new Map<string, Set<string>>();
    for (const t of data.transitions) {
      const set = map.get(t.from_state_id) ?? new Set<string>();
      set.add(t.to_state_id);
      map.set(t.from_state_id, set);
    }
    setDraft(map);
  }, [workflowQuery.data]);

  const states = workflowQuery.data?.states ?? [];
  const restricted = (workflowQuery.data?.transitions.length ?? 0) > 0;

  const dirty = useMemo(() => {
    if (!draft || !workflowQuery.data) return false;
    const server = new Map<string, Set<string>>();
    for (const t of workflowQuery.data.transitions) {
      const set = server.get(t.from_state_id) ?? new Set<string>();
      set.add(t.to_state_id);
      server.set(t.from_state_id, set);
    }
    if (server.size !== draft.size) return true;
    for (const [from, tos] of draft) {
      const s = server.get(from);
      if (!s || s.size !== tos.size) return true;
      for (const to of tos) if (!s.has(to)) return true;
    }
    return false;
  }, [draft, workflowQuery.data]);

  const saveMutation = useMutation({
    mutationFn: (transitions: WorkflowTransition[]) =>
      setWorkflow(slug, identifier, transitions),
    onSuccess: () => {
      setError(null);
      void queryClient.invalidateQueries({
        queryKey: ["workflow", slug, identifier],
      });
    },
    onError: (e) => setError(errMsg(e, "Failed to save workflow")),
  });

  if (workflowQuery.isPending || draft === null) {
    return <Skeleton className="h-48 w-full" />;
  }
  if (workflowQuery.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>
          {errMsg(workflowQuery.error, "Failed to load workflow")}
        </AlertDescription>
      </Alert>
    );
  }

  function toggle(fromId: string, toId: string) {
    setDraft((prev) => {
      if (!prev) return prev;
      const next = new Map(prev);
      const set = new Set(next.get(fromId) ?? []);
      if (set.has(toId)) set.delete(toId);
      else set.add(toId);
      if (set.size === 0) next.delete(fromId);
      else next.set(fromId, set);
      return next;
    });
  }

  function onSave() {
    const transitions: WorkflowTransition[] = [];
    for (const [from, tos] of draft ?? []) {
      for (const to of tos) transitions.push({ from_state_id: from, to_state_id: to });
    }
    saveMutation.mutate(transitions);
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Workflow</CardTitle>
        <p className="text-sm text-muted-foreground">
          Control which state moves are allowed. With no rules, any state can
          move to any other state. Denied moves are rejected with a 409 naming
          the permitted next states.
        </p>
      </CardHeader>
      <CardContent className="space-y-4">
        {!restricted && (
          <Alert>
            <AlertDescription>
              <strong>Unrestricted</strong> — any state can move to any other
              state. Select allowed transitions below to restrict movement.
            </AlertDescription>
          </Alert>
        )}
        <div className="space-y-3">
          {states.map((s) => {
            const allowed = draft.get(s.id) ?? new Set<string>();
            const targets = states.filter((t) => t.id !== s.id);
            return (
              <div
                key={s.id}
                data-testid={`workflow-from-${s.id}`}
                className="rounded-md border p-3"
              >
                <div className="mb-2 flex items-center gap-2">
                  <StateDot state={s} />
                  <span className="text-sm font-medium">{s.name}</span>
                  {restricted && allowed.size === 0 && (
                    <span className="text-xs text-muted-foreground">
                      — no moves out allowed
                    </span>
                  )}
                </div>
                <fieldset disabled={!canEdit}>
                  <legend className="sr-only">Allowed next states for {s.name}</legend>
                  <div className="flex flex-wrap gap-x-4 gap-y-1.5">
                    {targets.map((t) => (
                      <label
                        key={t.id}
                        className="flex cursor-pointer items-center gap-1.5 text-sm"
                      >
                        <input
                          type="checkbox"
                          className="h-4 w-4 accent-primary"
                          checked={allowed.has(t.id)}
                          onChange={() => toggle(s.id, t.id)}
                        />
                        <StateDot state={t} />
                        {t.name}
                      </label>
                    ))}
                  </div>
                </fieldset>
              </div>
            );
          })}
        </div>
        {error && (
          <Alert variant="destructive" role="alert">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <div className="flex items-center gap-2">
          <Button
            onClick={onSave}
            disabled={!canEdit || !dirty || saveMutation.isPending}
          >
            {saveMutation.isPending ? "Saving…" : "Save workflow"}
          </Button>
          {dirty && (
            <span className="text-xs text-muted-foreground">
              Unsaved changes
            </span>
          )}
        </div>
      </CardContent>
    </Card>
  );
}
