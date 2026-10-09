// Estimate scales management (C6T2): create scales with points, append
// points to a scale, delete scales. Deleting a scale whose points are
// referenced by issues is 409 (honest message). Mutations are member
// (15)+; the server is the gate.

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Trash2 } from "lucide-react";
import { ApiError } from "../../lib/api";
import {
  addEstimatePoints,
  createEstimate,
  deleteEstimate,
  fetchEstimates,
  taxonomyKeys,
  type Estimate,
} from "../../lib/taxonomy";
import { Alert, AlertDescription } from "../ui/alert";
import { Badge } from "../ui/badge";
import { Button } from "../ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../ui/card";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { Skeleton } from "../ui/skeleton";

function errMsg(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message : fallback;
}

interface PointDraft {
  key: string;
  value: string;
}

export default function EstimatesSection({
  slug,
  identifier,
  canEdit,
}: {
  slug: string;
  identifier: string;
  canEdit: boolean;
}) {
  const queryClient = useQueryClient();
  const keys = taxonomyKeys(slug, identifier);
  const [creating, setCreating] = useState(false);
  const [scaleName, setScaleName] = useState("");
  const [drafts, setDrafts] = useState<PointDraft[]>([{ key: "", value: "" }]);
  const [addingTo, setAddingTo] = useState<Estimate | null>(null);
  const [addDrafts, setAddDrafts] = useState<PointDraft[]>([{ key: "", value: "" }]);
  const [error, setError] = useState<string | null>(null);

  const estimatesQuery = useQuery({
    queryKey: keys.estimates,
    queryFn: () => fetchEstimates(slug, identifier),
  });
  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: keys.estimates });

  const createMutation = useMutation({
    mutationFn: () =>
      createEstimate(slug, identifier, {
        name: scaleName.trim(),
        points: toPoints(drafts),
      }),
    onSuccess: () => {
      setScaleName("");
      setDrafts([{ key: "", value: "" }]);
      setCreating(false);
      setError(null);
      void invalidate();
    },
    onError: (e) => setError(errMsg(e, "Failed to create scale")),
  });

  const addMutation = useMutation({
    mutationFn: () =>
      addEstimatePoints(slug, identifier, addingTo!.id, toPoints(addDrafts)),
    onSuccess: () => {
      setAddingTo(null);
      setAddDrafts([{ key: "", value: "" }]);
      setError(null);
      void invalidate();
    },
    onError: (e) => setError(errMsg(e, "Failed to add points")),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteEstimate(slug, identifier, id),
    onSuccess: () => {
      setError(null);
      void invalidate();
    },
    onError: (e) => setError(errMsg(e, "Failed to delete scale")),
  });

  function onDelete(e: Estimate) {
    if (
      !window.confirm(
        `Delete scale "${e.name}"? Scales in use by issues cannot be deleted.`,
      )
    )
      return;
    deleteMutation.mutate(e.id);
  }

  const estimates = estimatesQuery.data ?? [];

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle>Estimate scales</CardTitle>
        {canEdit && (
          <Button size="sm" onClick={() => setCreating(true)}>
            <Plus className="mr-1 h-4 w-4" /> New scale
          </Button>
        )}
      </CardHeader>
      <CardContent className="space-y-3">
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        {!canEdit && (
          <p className="text-sm text-muted-foreground">
            You are a guest here — estimate scales are read-only. Members can manage them.
          </p>
        )}
        {estimatesQuery.isLoading ? (
          <Skeleton className="h-16 w-full" />
        ) : estimates.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No estimate scales yet. Create one (e.g. Fibonacci) to enable
            story-point estimation.
          </p>
        ) : (
          <ul className="space-y-3">
            {estimates.map((e) => (
              <li key={e.id} className="rounded-md border p-3">
                <div className="flex items-center justify-between">
                  <span className="text-sm font-medium">{e.name}</span>
                  {canEdit && (
                    <div className="flex gap-1">
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => setAddingTo(e)}
                      >
                        <Plus className="mr-1 h-4 w-4" /> Points
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon"
                        aria-label={`Delete scale ${e.name}`}
                        onClick={() => onDelete(e)}
                      >
                        <Trash2 className="h-4 w-4" />
                      </Button>
                    </div>
                  )}
                </div>
                <div className="mt-2 flex flex-wrap gap-1">
                  {e.points.map((p) => (
                    <Badge key={p.id} variant="secondary" title={p.description ?? undefined}>
                      {p.key} · {p.value}
                    </Badge>
                  ))}
                </div>
              </li>
            ))}
          </ul>
        )}

        {/* Create scale dialog */}
        <Dialog open={creating} onOpenChange={setCreating}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>New estimate scale</DialogTitle>
            </DialogHeader>
            <div className="space-y-3">
              <div className="space-y-1">
                <Label htmlFor="scale-name">Name</Label>
                <Input
                  id="scale-name"
                  value={scaleName}
                  onChange={(e) => setScaleName(e.target.value)}
                  placeholder="Fibonacci"
                />
              </div>
              <PointDraftEditor drafts={drafts} setDrafts={setDrafts} />
            </div>
            <DialogFooter>
              <Button
                disabled={
                  !scaleName.trim() ||
                  toPoints(drafts).length === 0 ||
                  createMutation.isPending
                }
                onClick={() => createMutation.mutate()}
              >
                {createMutation.isPending ? "Creating…" : "Create scale"}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>

        {/* Add points dialog */}
        <Dialog open={addingTo !== null} onOpenChange={(o) => !o && setAddingTo(null)}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Add points to “{addingTo?.name}”</DialogTitle>
            </DialogHeader>
            <PointDraftEditor drafts={addDrafts} setDrafts={setAddDrafts} />
            <DialogFooter>
              <Button
                disabled={toPoints(addDrafts).length === 0 || addMutation.isPending}
                onClick={() => addMutation.mutate()}
              >
                {addMutation.isPending ? "Adding…" : "Add points"}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </CardContent>
    </Card>
  );
}

/** Parse drafts into valid points; rows with empty keys are ignored. */
export function toPoints(drafts: PointDraft[]): { key: string; value: number }[] {
  const out: { key: string; value: number }[] = [];
  for (const d of drafts) {
    const key = d.key.trim();
    const value = Number(d.value);
    if (key && Number.isFinite(value)) out.push({ key, value });
  }
  return out;
}

function PointDraftEditor({
  drafts,
  setDrafts,
}: {
  drafts: PointDraft[];
  setDrafts: (d: PointDraft[]) => void;
}) {
  function set(i: number, patch: Partial<PointDraft>) {
    setDrafts(drafts.map((d, j) => (j === i ? { ...d, ...patch } : d)));
  }
  return (
    <div className="space-y-2">
      <Label>Points</Label>
      {drafts.map((d, i) => (
        <div key={i} className="flex items-center gap-2">
          <Input
            value={d.key}
            onChange={(e) => set(i, { key: e.target.value })}
            placeholder="Key (e.g. 3)"
            className="w-28"
            aria-label={`Point ${i + 1} key`}
          />
          <Input
            value={d.value}
            onChange={(e) => set(i, { value: e.target.value })}
            placeholder="Value"
            inputMode="numeric"
            className="w-24"
            aria-label={`Point ${i + 1} value`}
          />
          {drafts.length > 1 && (
            <Button
              variant="ghost"
              size="icon"
              aria-label="Remove point row"
              onClick={() => setDrafts(drafts.filter((_, j) => j !== i))}
            >
              <Trash2 className="h-4 w-4" />
            </Button>
          )}
        </div>
      ))}
      <Button
        variant="outline"
        size="sm"
        onClick={() => setDrafts([...drafts, { key: "", value: "" }])}
      >
        <Plus className="mr-1 h-4 w-4" /> Add row
      </Button>
    </div>
  );
}
