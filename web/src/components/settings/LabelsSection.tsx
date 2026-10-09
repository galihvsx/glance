// Labels management (C6T2): workspace label CRUD for project settings.
// Delete cascades the issue_labels junction (backend ON DELETE CASCADE) —
// issues are untouched, the label is just detached. Mutations are member
// (15)+; the server is the gate.

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pencil, Plus, Trash2 } from "lucide-react";
import { ApiError } from "../../lib/api";
import {
  createLabel,
  deleteLabel,
  fetchLabels,
  isValidHexColor,
  taxonomyKeys,
  updateLabel,
  type TaxLabel,
} from "../../lib/taxonomy";
import { Alert, AlertDescription } from "../ui/alert";
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

export default function LabelsSection({
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
  const [name, setName] = useState("");
  const [color, setColor] = useState("#3b82f6");
  const [editing, setEditing] = useState<TaxLabel | null>(null);
  const [error, setError] = useState<string | null>(null);

  const labelsQuery = useQuery({
    queryKey: keys.labels,
    queryFn: () => fetchLabels(slug, identifier),
  });

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: keys.labels });

  const createMutation = useMutation({
    mutationFn: () => createLabel(slug, identifier, { name: name.trim(), color }),
    onSuccess: () => {
      setName("");
      setCreating(false);
      setError(null);
      void invalidate();
    },
    onError: (e) => setError(errMsg(e, "Failed to create label")),
  });

  const updateMutation = useMutation({
    mutationFn: (l: TaxLabel) =>
      updateLabel(slug, identifier, l.id, {
        name: editing?.name.trim(),
        color: editing?.color,
      }),
    onSuccess: () => {
      setEditing(null);
      setError(null);
      void invalidate();
    },
    onError: (e) => setError(errMsg(e, "Failed to update label")),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteLabel(slug, identifier, id),
    onSuccess: () => {
      setError(null);
      void invalidate();
    },
    onError: (e) => setError(errMsg(e, "Failed to delete label")),
  });

  function onDelete(l: TaxLabel) {
    if (
      !window.confirm(
        `Delete label "${l.name}"? It will be detached from all issues; the issues themselves are untouched.`,
      )
    )
      return;
    deleteMutation.mutate(l.id);
  }

  const labels = labelsQuery.data ?? [];

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle>Labels</CardTitle>
        {canEdit && (
          <Button size="sm" onClick={() => setCreating(true)}>
            <Plus className="mr-1 h-4 w-4" /> New label
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
            You are a guest here — labels are read-only. Members can manage them.
          </p>
        )}
        {labelsQuery.isLoading ? (
          <Skeleton className="h-10 w-full" />
        ) : labels.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No labels yet. Create one to start categorizing issues.
          </p>
        ) : (
          <ul className="divide-y rounded-md border">
            {labels.map((l) => (
              <li
                key={l.id}
                className="flex items-center gap-3 px-3 py-2"
              >
                <span
                  className="h-3.5 w-3.5 shrink-0 rounded-full border"
                  style={{ backgroundColor: l.color }}
                  aria-hidden
                />
                <span className="flex-1 text-sm font-medium">{l.name}</span>
                {canEdit && (
                  <div className="flex gap-1">
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={`Rename ${l.name}`}
                      onClick={() => setEditing(l)}
                    >
                      <Pencil className="h-4 w-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={`Delete ${l.name}`}
                      onClick={() => onDelete(l)}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </div>
                )}
              </li>
            ))}
          </ul>
        )}

        {/* Create dialog */}
        <Dialog open={creating} onOpenChange={setCreating}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>New label</DialogTitle>
            </DialogHeader>
            <div className="space-y-3">
              <div className="space-y-1">
                <Label htmlFor="label-name">Name</Label>
                <Input
                  id="label-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="bug"
                />
              </div>
              <div className="space-y-1">
                <Label htmlFor="label-color">Color</Label>
                <div className="flex items-center gap-2">
                  <Input
                    id="label-color"
                    value={color}
                    onChange={(e) => setColor(e.target.value)}
                    placeholder="#ef4444"
                    className="w-28 font-mono"
                  />
                  <span
                    className="h-6 w-6 rounded-full border"
                    style={{
                      backgroundColor: isValidHexColor(color) ? color : "transparent",
                    }}
                    aria-hidden
                  />
                </div>
                {!isValidHexColor(color) && (
                  <p className="text-xs text-destructive">
                    Use a hex color like #ef4444.
                  </p>
                )}
              </div>
            </div>
            <DialogFooter>
              <Button
                disabled={
                  !name.trim() ||
                  !isValidHexColor(color) ||
                  createMutation.isPending
                }
                onClick={() => createMutation.mutate()}
              >
                {createMutation.isPending ? "Creating…" : "Create label"}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>

        {/* Rename dialog */}
        <Dialog open={editing !== null} onOpenChange={(o) => !o && setEditing(null)}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Rename label</DialogTitle>
            </DialogHeader>
            {editing && (
              <div className="space-y-3">
                <div className="space-y-1">
                  <Label htmlFor="label-edit-name">Name</Label>
                  <Input
                    id="label-edit-name"
                    value={editing.name}
                    onChange={(e) =>
                      setEditing({ ...editing, name: e.target.value })
                    }
                  />
                </div>
                <div className="space-y-1">
                  <Label htmlFor="label-edit-color">Color</Label>
                  <Input
                    id="label-edit-color"
                    value={editing.color}
                    onChange={(e) =>
                      setEditing({ ...editing, color: e.target.value })
                    }
                    className="w-28 font-mono"
                  />
                  {!isValidHexColor(editing.color) && (
                    <p className="text-xs text-destructive">
                      Use a hex color like #ef4444.
                    </p>
                  )}
                </div>
              </div>
            )}
            <DialogFooter>
              <Button
                disabled={
                  !editing ||
                  !editing.name.trim() ||
                  !isValidHexColor(editing.color) ||
                  updateMutation.isPending
                }
                onClick={() => editing && updateMutation.mutate(editing)}
              >
                {updateMutation.isPending ? "Saving…" : "Save"}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </CardContent>
    </Card>
  );
}
