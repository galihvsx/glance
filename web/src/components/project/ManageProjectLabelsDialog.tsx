// ManageProjectLabelsDialog (C76T1): workspace-level project-label
// taxonomy CRUD — create / rename / recolor / delete. Deleting a label
// only detaches it from projects (junction rows); the projects are
// untouched. The delete confirm shows how many projects carry the label.
// Mutations are member (15)+; the server is the gate.

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pencil, Plus, Trash2 } from "lucide-react";
import { ApiError } from "../../lib/api";
import type { ProjectLabel } from "../../lib/types";
import { isValidHexColor } from "../../lib/taxonomy";
import {
  createProjectLabel,
  deleteProjectLabel,
  fetchProjectLabels,
  projectLabelKeys,
  updateProjectLabel,
} from "../../lib/projectLabels";
import { Alert, AlertDescription } from "../ui/alert";
import { Button } from "../ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
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

const DEFAULT_COLOR = "#3b82f6";

export default function ManageProjectLabelsDialog({
  slug,
  open,
  onOpenChange,
  /** Label id → number of projects carrying it (for the delete confirm). */
  projectUsage,
  canEdit,
  /** Called after any successful mutation (the Projects page reloads its
   *  plain-fetch list; react-query surfaces are invalidated internally). */
  onChanged,
}: {
  slug: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  projectUsage: Map<string, number>;
  canEdit: boolean;
  onChanged?: () => void;
}) {
  const queryClient = useQueryClient();
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [color, setColor] = useState(DEFAULT_COLOR);
  const [editing, setEditing] = useState<ProjectLabel | null>(null);
  const [error, setError] = useState<string | null>(null);

  const labelsQuery = useQuery({
    queryKey: projectLabelKeys(slug).labels,
    queryFn: () => fetchProjectLabels(slug),
    enabled: open,
  });

  const invalidate = () => {
    void queryClient.invalidateQueries({
      queryKey: projectLabelKeys(slug).labels,
    });
    // Project payloads embed label chips — renames/recolors/deletes
    // change every list row carrying the label.
    void queryClient.invalidateQueries({ queryKey: ["projects", slug] });
    onChanged?.();
  };

  const createMutation = useMutation({
    mutationFn: () =>
      createProjectLabel(slug, { name: name.trim(), color }),
    onSuccess: () => {
      setName("");
      setColor(DEFAULT_COLOR);
      setCreating(false);
      setError(null);
      invalidate();
    },
    onError: (e) => setError(errMsg(e, "Failed to create label")),
  });

  const updateMutation = useMutation({
    mutationFn: (l: ProjectLabel) =>
      updateProjectLabel(slug, l.id, {
        name: editing?.name.trim(),
        color: editing?.color,
      }),
    onSuccess: () => {
      setEditing(null);
      setError(null);
      invalidate();
    },
    onError: (e) => setError(errMsg(e, "Failed to update label")),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteProjectLabel(slug, id),
    onSuccess: () => {
      setError(null);
      invalidate();
    },
    onError: (e) => setError(errMsg(e, "Failed to delete label")),
  });

  function onDelete(l: ProjectLabel) {
    const n = projectUsage.get(l.id) ?? 0;
    const usage =
      n === 0
        ? "No project carries it yet."
        : `It is currently on ${n} project${n === 1 ? "" : "s"}.`;
    if (
      !window.confirm(
        `Delete label "${l.name}"? ${usage} The label will be detached; the projects themselves are untouched.`,
      )
    )
      return;
    deleteMutation.mutate(l.id);
  }

  const labels = [...(labelsQuery.data ?? [])].sort((a, b) =>
    a.name.localeCompare(b.name),
  );

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Manage project labels</DialogTitle>
          <DialogDescription>
            Workspace-level labels, reusable across projects. Guests can see
            them but cannot change them.
          </DialogDescription>
        </DialogHeader>

        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}

        {labelsQuery.isPending ? (
          <div className="space-y-2">
            <Skeleton className="h-10 w-full" />
            <Skeleton className="h-10 w-full" />
          </div>
        ) : labels.length === 0 ? (
          <p className="py-4 text-center text-sm text-muted-foreground">
            No project labels yet. Create one to start organizing projects.
          </p>
        ) : (
          <ul
            data-testid="project-label-list"
            className="max-h-72 divide-y divide-border-hairline overflow-y-auto rounded-md border border-border-hairline"
          >
            {labels.map((l) => (
              <li
                key={l.id}
                className="flex items-center gap-3 px-3 py-2 hover:bg-wash-hover"
              >
                <span
                  className="h-3.5 w-3.5 shrink-0 rounded-full border"
                  style={{ backgroundColor: l.color }}
                  aria-hidden
                />
                <span className="flex-1 truncate font-medium">{l.name}</span>
                {(projectUsage.get(l.id) ?? 0) > 0 && (
                  <span className="shrink-0 text-[11px] tabular-nums text-muted-foreground">
                    {projectUsage.get(l.id)} project
                    {(projectUsage.get(l.id) ?? 0) === 1 ? "" : "s"}
                  </span>
                )}
                {canEdit && (
                  <div className="flex shrink-0 gap-1">
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
                      disabled={deleteMutation.isPending}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </div>
                )}
              </li>
            ))}
          </ul>
        )}

        {canEdit && (
          <DialogFooter>
            <Button size="sm" onClick={() => setCreating(true)}>
              <Plus className="mr-1 h-4 w-4" /> New label
            </Button>
          </DialogFooter>
        )}

        {/* Create dialog */}
        <Dialog open={creating} onOpenChange={setCreating}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>New project label</DialogTitle>
            </DialogHeader>
            <div className="space-y-3">
              <div className="space-y-1">
                <Label htmlFor="pl-name">Name</Label>
                <Input
                  id="pl-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="platform"
                />
              </div>
              <div className="space-y-1">
                <Label htmlFor="pl-color">Color</Label>
                <div className="flex items-center gap-2">
                  <Input
                    id="pl-color"
                    value={color}
                    onChange={(e) => setColor(e.target.value)}
                    placeholder="#ef4444"
                    className="w-28 font-mono"
                  />
                  <span
                    className="h-6 w-6 rounded-full border"
                    style={{
                      backgroundColor: isValidHexColor(color)
                        ? color
                        : "transparent",
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

        {/* Rename / recolor dialog */}
        <Dialog
          open={editing !== null}
          onOpenChange={(o) => !o && setEditing(null)}
        >
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Rename project label</DialogTitle>
            </DialogHeader>
            {editing && (
              <div className="space-y-3">
                <div className="space-y-1">
                  <Label htmlFor="pl-edit-name">Name</Label>
                  <Input
                    id="pl-edit-name"
                    value={editing.name}
                    onChange={(e) =>
                      setEditing({ ...editing, name: e.target.value })
                    }
                  />
                </div>
                <div className="space-y-1">
                  <Label htmlFor="pl-edit-color">Color</Label>
                  <Input
                    id="pl-edit-color"
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
      </DialogContent>
    </Dialog>
  );
}
