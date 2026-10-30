// TeamspaceManageDialog (C89T1): the sidebar teamspace manage dialog
// (prototype-as-spec): create, rename, and delete with a confirm step.
// Create/rename reject an empty (whitespace-only) name inline, before
// any API call. Delete confirms with the LIVE assigned-project count
// from the sidebar project list: "N projects will move to Ungrouped —
// no projects will be deleted." (The delete response counts archived
// rows too, but that count only exists after the delete runs, so the
// confirm — which must precede it — uses the live count; the two
// agree whenever no archived project is assigned to the teamspace.)
// After any mutation the caller invalidates ["projects", slug] and
// ["teamspaces", slug] via the shared invalidate helper here.

import { useEffect, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../../lib/api";
import type { Project, Teamspace } from "../../lib/types";
import {
  createTeamspace,
  deleteTeamspace,
  teamspaceKeys,
  updateTeamspace,
} from "../../lib/teamspaces";
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

export type TeamspaceDialogState =
  | { mode: "create" }
  | { mode: "rename"; teamspace: Teamspace }
  | { mode: "delete"; teamspace: Teamspace };

function errMsg(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message : fallback;
}

export default function TeamspaceManageDialog({
  slug,
  state,
  projects,
  onClose,
}: {
  slug: string | null;
  state: TeamspaceDialogState | null;
  /** Live sidebar project list, for the delete-confirm count. */
  projects: Project[];
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);

  // Reset the form whenever a new dialog state opens (rename prefills).
  useEffect(() => {
    if (state?.mode === "rename") setName(state.teamspace.name);
    else setName("");
    setError(null);
  }, [state]);

  const invalidate = () => {
    if (!slug) return;
    void queryClient.invalidateQueries({ queryKey: teamspaceKeys(slug).list });
    void queryClient.invalidateQueries({ queryKey: ["projects", slug] });
  };

  const saveMutation = useMutation({
    mutationFn: () => {
      if (!slug) throw new Error("no workspace");
      if (state?.mode === "rename") {
        return updateTeamspace(slug, state.teamspace.id, { name: name.trim() });
      }
      return createTeamspace(slug, { name: name.trim() });
    },
    onSuccess: () => {
      setError(null);
      invalidate();
      onClose();
    },
    onError: (e) =>
      setError(
        errMsg(
          e,
          state?.mode === "rename"
            ? "Failed to rename teamspace"
            : "Failed to create teamspace",
        ),
      ),
  });

  const deleteMutation = useMutation({
    mutationFn: () => {
      if (!slug || state?.mode !== "delete") throw new Error("no teamspace");
      return deleteTeamspace(slug, state.teamspace.id);
    },
    onSuccess: () => {
      setError(null);
      invalidate();
      onClose();
    },
    onError: (e) => setError(errMsg(e, "Failed to delete teamspace")),
  });

  const open = state !== null && slug !== null;

  function submitName() {
    if (name.trim() === "") {
      setError("Teamspace name is required.");
      return;
    }
    setError(null);
    saveMutation.mutate();
  }

  if (state?.mode === "delete") {
    const liveCount = projects.filter(
      (p) => !p.archived_at && p.teamspace_id === state.teamspace.id,
    ).length;
    return (
      <Dialog open={open} onOpenChange={(v) => !v && onClose()}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              Delete teamspace &ldquo;{state.teamspace.name}&rdquo;?
            </DialogTitle>
            <DialogDescription>
              {liveCount} {liveCount === 1 ? "project" : "projects"} will
              move to Ungrouped — no projects will be deleted.
            </DialogDescription>
          </DialogHeader>
          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              disabled={deleteMutation.isPending}
              onClick={() => deleteMutation.mutate()}
            >
              {deleteMutation.isPending ? "Deleting…" : "Delete teamspace"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    );
  }

  const isRename = state?.mode === "rename";
  return (
    <Dialog open={open} onOpenChange={(v) => !v && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {isRename ? "Rename teamspace" : "New teamspace"}
          </DialogTitle>
          <DialogDescription>
            {isRename
              ? "Rename this teamspace. Projects stay where they are."
              : "Group projects in the sidebar under this teamspace."}
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-2">
          <Label htmlFor="teamspace-name">Name</Label>
          <Input
            id="teamspace-name"
            value={name}
            maxLength={120}
            onChange={(e) => {
              setName(e.target.value);
              setError(null);
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter") submitName();
            }}
          />
        </div>
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button
            disabled={saveMutation.isPending}
            onClick={submitName}
          >
            {saveMutation.isPending
              ? "Saving…"
              : isRename
                ? "Save changes"
                : "Create teamspace"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
