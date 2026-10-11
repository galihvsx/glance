// Project danger zone (C18T3): archive/restore and hard delete.
// Admin (20) only — the server 403s any non-admin attempt; this section
// is rendered only when the caller is an admin (ProjectSettings gates on
// ws.role === 20).

import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../../lib/api";
import type { Project } from "../../lib/types";
import { Alert, AlertDescription } from "../ui/alert";
import { Button } from "../ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "../ui/card";
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

function projectKey(slug: string, identifier: string) {
  return ["project", slug, identifier];
}

function errMsg(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message : fallback;
}

export default function DangerZoneSection({
  slug,
  identifier,
}: {
  slug: string;
  identifier: string;
}) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const base = `/api/v1/workspaces/${encodeURIComponent(slug)}/projects/${encodeURIComponent(identifier)}`;

  const query = useQuery({
    queryKey: projectKey(slug, identifier),
    queryFn: () => api.get<Project>(base),
  });

  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);

  // Delete dialog state.
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [confirmText, setConfirmText] = useState("");
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  // Set when the server refuses with 409: the project still has live
  // issues, and the dialog offers an explicit force-delete path.
  const [blockedCount, setBlockedCount] = useState<number | null>(null);

  const project = query.data ?? null;
  const isArchived = Boolean(project?.archived_at);

  function refreshLists() {
    void queryClient.invalidateQueries({ queryKey: projectKey(slug, identifier) });
    void queryClient.invalidateQueries({ queryKey: ["projects", slug] });
  }

  async function onArchiveToggle() {
    if (!project || busy) return;
    setBusy(true);
    setActionError(null);
    try {
      await api.post<Project>(
        `${base}/${isArchived ? "unarchive" : "archive"}`,
      );
      refreshLists();
      if (!isArchived) {
        // The project just left the live lists — take the admin back to
        // the projects page, where it now sits in the Archived section.
        navigate(`/w/${encodeURIComponent(slug)}/projects`);
      }
    } catch (err) {
      setActionError(
        errMsg(err, isArchived ? "Failed to restore project" : "Failed to archive project"),
      );
    } finally {
      setBusy(false);
    }
  }

  function openDeleteDialog() {
    setConfirmText("");
    setDeleteError(null);
    setBlockedCount(null);
    setConfirmOpen(true);
  }

  async function runDelete(force: boolean) {
    if (!project) return;
    // The typed confirmation is enforced client-side (button stays
    // disabled until the text matches) AND server-side (?confirm=).
    if (confirmText.trim().toUpperCase() !== project.identifier.toUpperCase())
      return;
    setDeleting(true);
    setDeleteError(null);
    try {
      const qs = new URLSearchParams({ confirm: project.identifier });
      if (force) qs.set("force", "true");
      await api.del(`${base}?${qs.toString()}`);
      refreshLists();
      setConfirmOpen(false);
      navigate(`/w/${encodeURIComponent(slug)}/projects`, { replace: true });
    } catch (err) {
      if (
        err instanceof ApiError &&
        err.status === 409 &&
        typeof err.details === "object" &&
        err.details !== null &&
        "non_archived_issues" in err.details
      ) {
        const n = (err.details as { non_archived_issues?: unknown })
          .non_archived_issues;
        setBlockedCount(typeof n === "number" ? n : null);
      }
      setDeleteError(errMsg(err, "Failed to delete project"));
    } finally {
      setDeleting(false);
    }
  }

  async function onDelete(e: FormEvent) {
    e.preventDefault();
    await runDelete(false);
  }

  if (query.isLoading) {
    return <Skeleton className="h-48 w-full" />;
  }

  return (
    <div className="space-y-4">
      {actionError && (
        <Alert variant="destructive">
          <AlertDescription>{actionError}</AlertDescription>
        </Alert>
      )}

      {/* ---------- Archive / restore ---------- */}
      <Card className="border-destructive/50">
        <CardHeader>
          <CardTitle className="text-destructive">Danger zone</CardTitle>
          <CardDescription>
            {isArchived
              ? "This project is archived: hidden from the sidebar, project list and search, and its issues are read-only (archived). Restoring brings it back."
              : "Archiving hides the project from the sidebar, project list and search, and archives all its issues in one transaction. Restoring reverses it."}
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-wrap items-center gap-3">
          <Button
            variant={isArchived ? "default" : "outline"}
            onClick={() => void onArchiveToggle()}
            disabled={busy || !project}
          >
            {busy
              ? isArchived
                ? "Restoring…"
                : "Archiving…"
              : isArchived
                ? "Restore project"
                : "Archive project"}
          </Button>
          <Button variant="destructive" onClick={openDeleteDialog} disabled={!project}>
            Delete project
          </Button>
        </CardContent>
      </Card>

      <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete “{project?.name}”?</DialogTitle>
            <DialogDescription>
              This permanently deletes the project and everything in it —
              issues, cycles, modules, pages, views and links. This cannot
              be undone. Type{" "}
              <span className="font-mono font-semibold">
                {project?.identifier}
              </span>{" "}
              to confirm.
            </DialogDescription>
          </DialogHeader>
          <form onSubmit={(e) => void onDelete(e)} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="danger-confirm-identifier">
                Project identifier
              </Label>
              <Input
                id="danger-confirm-identifier"
                value={confirmText}
                onChange={(e) => setConfirmText(e.target.value)}
                placeholder={project?.identifier}
                autoComplete="off"
              />
            </div>
            {deleteError && (
              <Alert variant="destructive">
                <AlertDescription>
                  {deleteError}
                  {blockedCount !== null && (
                    <span className="mt-2 block">
                      {blockedCount} non-archived issue
                      {blockedCount === 1 ? "" : "s"} still live. Archive or
                      delete them first, or force-delete everything now.
                    </span>
                  )}
                </AlertDescription>
              </Alert>
            )}
            <DialogFooter className="gap-2">
              <Button
                type="button"
                variant="outline"
                onClick={() => setConfirmOpen(false)}
              >
                Cancel
              </Button>
              {blockedCount !== null && (
                <Button
                  type="button"
                  variant="destructive"
                  disabled={
                    deleting ||
                    confirmText.trim().toUpperCase() !==
                      (project?.identifier ?? "").toUpperCase()
                  }
                  onClick={() => void runDelete(true)}
                >
                  {deleting ? "Deleting…" : "Force delete anyway"}
                </Button>
              )}
              <Button
                type="submit"
                variant="destructive"
                disabled={
                  deleting ||
                  confirmText.trim().toUpperCase() !==
                    (project?.identifier ?? "").toUpperCase()
                }
              >
                {deleting ? "Deleting…" : "Delete project"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  );
}
