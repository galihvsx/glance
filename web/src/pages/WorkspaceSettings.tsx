import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, ApiError } from "../lib/api";
import type { Workspace, WorkspaceMember } from "../lib/types";
import { roleLabel, WORKSPACE_ROLES } from "../lib/types";
import { Button } from "../components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "../components/ui/card";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { Badge } from "../components/ui/badge";
import { Skeleton } from "../components/ui/skeleton";
import { Alert, AlertDescription } from "../components/ui/alert";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../components/ui/dialog";
import ThemeToggle from "../components/ThemeToggle";
import NotificationBell from "../components/notifications/NotificationBell";
import WebhooksSection from "../components/settings/WebhooksSection";

// Mirrors the backend contract (service.validSlug): lowercase alphanumeric
// groups joined by single hyphens, 1–64 chars. Same rule as onboarding.
const SLUG_RE = /^[a-z0-9]+(-[a-z0-9]+)*$/;

function slugError(slug: string): string | null {
  if (!slug) return "Slug is required.";
  if (slug.length > 64 || !SLUG_RE.test(slug))
    return "Use lowercase letters, numbers and hyphens (e.g. acme-inc).";
  return null;
}

export default function WorkspaceSettings() {
  const { slug } = useParams<{ slug: string }>();
  const navigate = useNavigate();

  const [ws, setWs] = useState<(Workspace & { role: number }) | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);

  // General section state
  const [name, setName] = useState("");
  const [newSlug, setNewSlug] = useState("");
  const [slugTouched, setSlugTouched] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveMsg, setSaveMsg] = useState<{ ok: boolean; text: string } | null>(
    null,
  );

  // Members section state
  const [members, setMembers] = useState<WorkspaceMember[] | null>(null);
  const [memberError, setMemberError] = useState<string | null>(null);
  const [actingId, setActingId] = useState<string | null>(null);

  // Danger zone state
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [confirmText, setConfirmText] = useState("");
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const wsPath = (s: string) => `/api/v1/workspaces/${encodeURIComponent(s)}`;

  const load = useCallback(async () => {
    if (!slug) return;
    setLoadError(null);
    try {
      const data = await api.get<Workspace & { role: number }>(wsPath(slug));
      setWs(data);
      setName(data.name);
      setNewSlug(data.slug);
      setSlugTouched(false);
      const m = await api.get<{ members: WorkspaceMember[] }>(
        `${wsPath(slug)}/members`,
      );
      setMembers(m.members);
    } catch (e) {
      setLoadError(
        e instanceof ApiError ? e.message : "Failed to load workspace",
      );
    }
  }, [slug]);

  useEffect(() => {
    void load();
  }, [load]);

  const isAdmin = ws?.role === 20;
  const slugMsg = slugTouched ? slugError(newSlug) : null;
  const slugChanged = ws !== null && newSlug !== ws.slug;
  const nameChanged = ws !== null && name.trim() !== ws.name;
  const canSave =
    isAdmin &&
    name.trim() !== "" &&
    slugError(newSlug) === null &&
    (nameChanged || slugChanged) &&
    !saving;

  async function onSave(e: FormEvent) {
    e.preventDefault();
    if (!slug || !ws || !canSave) return;
    setSaving(true);
    setSaveMsg(null);
    try {
      const updated = await api.patch<Workspace & { role: number }>(
        wsPath(slug),
        {
          name: name.trim(),
          // Only send slug when it actually changed — the backend treats
          // an absent key as "keep".
          ...(slugChanged ? { slug: newSlug.trim() } : {}),
        },
      );
      setWs(updated);
      setName(updated.name);
      setNewSlug(updated.slug);
      setSlugTouched(false);
      setSaveMsg({ ok: true, text: "Workspace updated." });
      // The slug is part of every URL: if it changed, move there so the
      // page (and the browser history) stays on a real route.
      if (updated.slug !== slug) {
        navigate(`/w/${updated.slug}/settings`, { replace: true });
      }
    } catch (err) {
      setSaveMsg({
        ok: false,
        text:
          err instanceof ApiError ? err.message : "Failed to update workspace",
      });
    } finally {
      setSaving(false);
    }
  }

  async function onRoleChange(member: WorkspaceMember, role: number) {
    if (!slug || role === member.role) return;
    setActingId(member.id);
    setMemberError(null);
    try {
      await api.post(`${wsPath(slug)}/members`, {
        user_id: member.id,
        role,
      });
      setMembers((prev) =>
        prev?.map((m) => (m.id === member.id ? { ...m, role } : m )) ?? prev,
      );
    } catch (err) {
      // 409 "cannot remove or demote the last admin" surfaces verbatim.
      setMemberError(
        err instanceof ApiError ? err.message : "Failed to change role",
      );
    } finally {
      setActingId(null);
    }
  }

  async function onRemove(member: WorkspaceMember) {
    if (!slug) return;
    if (
      !window.confirm(
        `Remove ${member.name ?? member.email} from this workspace?`,
      )
    )
      return;
    setActingId(member.id);
    setMemberError(null);
    try {
      await api.del(
        `${wsPath(slug)}/members/${encodeURIComponent(member.id)}`,
      );
      setMembers((prev) => prev?.filter((m) => m.id !== member.id) ?? prev);
    } catch (err) {
      setMemberError(
        err instanceof ApiError ? err.message : "Failed to remove member",
      );
    } finally {
      setActingId(null);
    }
  }

  async function onDelete(e: FormEvent) {
    e.preventDefault();
    if (!slug || !ws || confirmText !== ws.slug) return;
    setDeleting(true);
    setDeleteError(null);
    try {
      await api.del(wsPath(slug));
      navigate("/w", { replace: true });
    } catch (err) {
      setDeleteError(
        err instanceof ApiError ? err.message : "Failed to delete workspace",
      );
    } finally {
      setDeleting(false);
    }
  }

  if (loadError) {
    return (
      <div className="mx-auto w-full max-w-3xl p-6">
        <Alert variant="destructive">
          <AlertDescription>{loadError}</AlertDescription>
        </Alert>
        <Link to="/w" className="text-sm text-muted-foreground hover:underline">
          ← Back to workspaces
        </Link>
      </div>
    );
  }

  if (!ws) {
    return (
      <div className="mx-auto w-full max-w-3xl space-y-3 p-6">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-40 w-full" />
        <Skeleton className="h-40 w-full" />
      </div>
    );
  }

  return (
    <div className="mx-auto w-full max-w-3xl p-6">
      <div className="mb-6 flex items-center justify-between">
        <div>
          <Link
            to={`/w/${ws.slug}`}
            className="text-xs text-muted-foreground hover:underline"
          >
            ← {ws.name}
          </Link>
          <h1 className="text-2xl font-semibold tracking-tight">
            Workspace settings
          </h1>
          <p className="text-sm text-muted-foreground">
            Manage this workspace, its members, webhooks and danger zone.
          </p>
        </div>
        <NotificationBell />
        <ThemeToggle />
      </div>

      {!isAdmin && (
        <Alert className="mb-4">
          <AlertDescription>
            You are a {roleLabel(ws.role).toLowerCase()} here — only admins can
            change settings.
          </AlertDescription>
        </Alert>
      )}

      {/* ---------- General ---------- */}
      <Card className="mb-4">
        <CardHeader>
          <CardTitle>General</CardTitle>
          <CardDescription>
            The name and URL slug for this workspace.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={onSave} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="ws-name">Name</Label>
              <Input
                id="ws-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                disabled={!isAdmin}
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="ws-slug">Slug</Label>
              <Input
                id="ws-slug"
                value={newSlug}
                onChange={(e) => {
                  setSlugTouched(true);
                  setNewSlug(e.target.value.toLowerCase());
                }}
                disabled={!isAdmin}
                required
              />
              {slugMsg ? (
                <p className="text-xs text-destructive">{slugMsg}</p>
              ) : (
                <p className="text-xs text-muted-foreground">
                  Lowercase letters, numbers and hyphens. Used in URLs —{" "}
                  {slugChanged
                    ? "changing it moves every link to this workspace."
                    : "links update automatically if you change it."}
                </p>
              )}
            </div>
            {saveMsg && (
              <Alert variant={saveMsg.ok ? "default" : "destructive"}>
                <AlertDescription>{saveMsg.text}</AlertDescription>
              </Alert>
            )}
            {isAdmin && (
              <Button type="submit" disabled={!canSave}>
                {saving ? "Saving…" : "Save changes"}
              </Button>
            )}
          </form>
        </CardContent>
      </Card>

      {/* ---------- Members ---------- */}
      <Card className="mb-4">
        <CardHeader>
          <CardTitle>Members</CardTitle>
          <CardDescription>
            {members?.length ?? 0} member
            {members?.length === 1 ? "" : "s"} in this workspace.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {memberError && (
            <Alert variant="destructive" className="mb-3">
              <AlertDescription>{memberError}</AlertDescription>
            </Alert>
          )}
          {members === null ? (
            <div className="space-y-2">
              <Skeleton className="h-10 w-full" />
              <Skeleton className="h-10 w-full" />
            </div>
          ) : (
            <ul className="divide-y divide-border">
              {members.map((m) => (
                <li
                  key={m.id}
                  className="flex items-center justify-between gap-3 py-2.5"
                >
                  <div className="min-w-0">
                    <p className="truncate text-sm font-medium">
                      {m.name ?? m.email}
                    </p>
                    {m.name && (
                      <p className="truncate text-xs text-muted-foreground">
                        {m.email}
                      </p>
                    )}
                  </div>
                  <div className="flex items-center gap-2">
                    {isAdmin ? (
                      <>
                        <select
                          aria-label={`Role for ${m.name ?? m.email}`}
                          value={m.role}
                          disabled={actingId === m.id}
                          onChange={(e) =>
                            onRoleChange(m, Number(e.target.value))
                          }
                          className="rounded-md border border-input bg-background px-2 py-1 text-sm"
                        >
                          {WORKSPACE_ROLES.map((r) => (
                            <option key={r.value} value={r.value}>
                              {r.label}
                            </option>
                          ))}
                        </select>
                        <Button
                          variant="ghost"
                          size="sm"
                          disabled={actingId === m.id}
                          onClick={() => onRemove(m)}
                        >
                          {actingId === m.id ? "…" : "Remove"}
                        </Button>
                      </>
                    ) : (
                      <Badge variant="secondary">{roleLabel(m.role)}</Badge>
                    )}
                  </div>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>

      {/* ---------- Webhooks (admin only, matches danger-zone gating) ---------- */}
      {isAdmin && <WebhooksSection slug={ws.slug} />}

      {/* ---------- Danger zone ---------- */}
      {isAdmin && (
        <Card className="border-destructive/50">
          <CardHeader>
            <CardTitle className="text-destructive">Danger zone</CardTitle>
            <CardDescription>
              Deleting a workspace removes its projects, issues, pages and
              members. This cannot be undone.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Button
              variant="destructive"
              onClick={() => {
                setConfirmText("");
                setDeleteError(null);
                setConfirmOpen(true);
              }}
            >
              Delete workspace
            </Button>
          </CardContent>
        </Card>
      )}

      <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete “{ws.name}”?</DialogTitle>
            <DialogDescription>
              This permanently deletes the workspace and everything in it.
              Type <span className="font-mono font-semibold">{ws.slug}</span>{" "}
              to confirm.
            </DialogDescription>
          </DialogHeader>
          <form onSubmit={onDelete} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="confirm-slug">Workspace slug</Label>
              <Input
                id="confirm-slug"
                value={confirmText}
                onChange={(e) => setConfirmText(e.target.value)}
                placeholder={ws.slug}
                autoComplete="off"
              />
            </div>
            {deleteError && (
              <Alert variant="destructive">
                <AlertDescription>{deleteError}</AlertDescription>
              </Alert>
            )}
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={() => setConfirmOpen(false)}
              >
                Cancel
              </Button>
              <Button
                type="submit"
                variant="destructive"
                disabled={deleting || confirmText !== ws.slug}
              >
                {deleting ? "Deleting…" : "Delete workspace"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  );
}
