// Workspace picker (C19T9 polish): dense 47px workspace rows in the
// token ramp — name 13px/600, /slug 11px muted, role chip, admin
// settings action. Shell 49px header bar replaces the oversized h1
// (no workspace scope, so no PageHeader breadcrumb); "New workspace"
// moves to the header's primary button. Token empty state.
import { useCallback, useEffect, useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { ChevronRight, Layers, Plus } from "lucide-react";
import { api, ApiError } from "../lib/api";
import type { Workspace } from "../lib/types";
import { roleLabel } from "../lib/types";
import { Button } from "../components/ui/button";
import EmptyState from "../components/personal/EmptyState";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../components/ui/dialog";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { Skeleton } from "../components/ui/skeleton";
import { Alert, AlertDescription } from "../components/ui/alert";

function slugify(name: string): string {
  return name
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

function WorkspaceRow({
  ws,
  onOpen,
  onSettings,
}: {
  ws: Workspace;
  onOpen: () => void;
  onSettings: () => void;
}) {
  return (
    <li>
      <div
        role="link"
        tabIndex={0}
        aria-label={`Open workspace ${ws.name}`}
        onClick={onOpen}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            onOpen();
          }
        }}
        className="flex min-h-[47px] w-full cursor-pointer items-center gap-3 px-3 py-2 transition-colors hover:bg-wash-hover"
      >
        <span className="flex min-w-0 flex-1 items-baseline gap-2">
          <span className="truncate text-[13px] font-semibold text-ink">
            {ws.name}
          </span>
          <span className="shrink-0 font-mono text-[10px] text-ink-muted">
            /{ws.slug}
          </span>
        </span>
        <span className="shrink-0 rounded border border-border px-1.5 py-0.5 text-[10px] leading-none text-ink-secondary">
          {roleLabel(ws.role)}
        </span>
        {ws.role === 20 && (
          <Button
            variant="ghost"
            size="sm"
            onClick={(e) => {
              e.stopPropagation();
              onSettings();
            }}
            onKeyDown={(e) => e.stopPropagation()}
            aria-label={`Workspace settings for ${ws.name}`}
          >
            Settings
          </Button>
        )}
        <ChevronRight
          className="h-3.5 w-3.5 shrink-0 text-ink-muted"
          aria-hidden="true"
        />
      </div>
    </li>
  );
}

export default function Workspaces() {
  const navigate = useNavigate();
  const [workspaces, setWorkspaces] = useState<Workspace[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [slugTouched, setSlugTouched] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  const load = useCallback(async () => {
    setError(null);
    try {
      // Wrapped shape: {"workspaces": [...]}
      const data = await api.get<{ workspaces: Workspace[] }>(
        "/api/v1/workspaces",
      );
      setWorkspaces(data.workspaces);
    } catch (e) {
      setWorkspaces([]);
      setError(e instanceof ApiError ? e.message : "Failed to load workspaces");
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  function onNameChange(value: string) {
    setName(value);
    if (!slugTouched) setSlug(slugify(value));
  }

  async function onCreate(e: FormEvent) {
    e.preventDefault();
    setFormError(null);
    setCreating(true);
    try {
      const ws = await api.post<Workspace>("/api/v1/workspaces", {
        name: name.trim(),
        slug: slug.trim(),
      });
      setDialogOpen(false);
      setName("");
      setSlug("");
      setSlugTouched(false);
      // Jump straight into the new workspace.
      navigate(`/w/${ws.slug}`);
    } catch (err) {
      // 409 slug conflict surfaces the API's message verbatim.
      setFormError(
        err instanceof ApiError ? err.message : "Failed to create workspace",
      );
    } finally {
      setCreating(false);
    }
  }

  return (
    <div className="w-full">
      {/* Shell 49px header bar — no workspace scope, so no PageHeader
          breadcrumb. */}
      <div className="flex h-[49px] shrink-0 items-center gap-2.5 border-b border-border bg-[#0c0c0e] px-3 pl-4">
        <span className="text-xs font-semibold text-ink">Workspaces</span>
        {workspaces && workspaces.length > 0 && (
          <span className="text-xs text-ink-muted">{workspaces.length}</span>
        )}
        <div className="ml-auto">
          <Button size="sm" onClick={() => setDialogOpen(true)}>
            <Plus className="mr-1.5 h-3.5 w-3.5" aria-hidden="true" />
            New workspace
          </Button>
        </div>
      </div>

      {/* The create dialog lives at page level so the header button and
          the empty state can both open it. */}
      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>New workspace</DialogTitle>
            <DialogDescription>
              Workspaces hold projects. You can invite members later.
            </DialogDescription>
          </DialogHeader>
          <form onSubmit={onCreate} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="ws-name">Name</Label>
              <Input
                id="ws-name"
                value={name}
                onChange={(e) => onNameChange(e.target.value)}
                placeholder="Acme Inc"
                required
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="ws-slug">Slug</Label>
              <Input
                id="ws-slug"
                value={slug}
                onChange={(e) => {
                  setSlugTouched(true);
                  setSlug(slugify(e.target.value));
                }}
                placeholder="acme-inc"
                required
              />
              <p className="text-xs text-ink-muted">
                Lowercase letters, numbers and hyphens. Used in URLs.
              </p>
            </div>
            {formError && (
              <Alert variant="destructive">
                <AlertDescription>{formError}</AlertDescription>
              </Alert>
            )}
            <DialogFooter>
              <Button type="submit" disabled={creating || !name || !slug}>
                {creating ? "Creating…" : "Create workspace"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <div className="mx-auto w-full max-w-3xl space-y-4 p-6">
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}

        {workspaces === null ? (
          <div className="space-y-2">
            <Skeleton className="h-[47px] w-full" />
            <Skeleton className="h-[47px] w-full" />
            <Skeleton className="h-[47px] w-full" />
          </div>
        ) : workspaces.length === 0 ? (
          <EmptyState icon={Layers} title="No workspaces yet">
            <p>Create your first workspace to start tracking issues.</p>
            <Button
              variant="outline"
              size="sm"
              className="mt-3"
              onClick={() => setDialogOpen(true)}
            >
              <Plus className="mr-2 h-4 w-4" /> New workspace
            </Button>
          </EmptyState>
        ) : (
          <ul className="divide-y divide-border-hairline overflow-hidden rounded-md border border-border">
            {workspaces.map((ws) => (
              <WorkspaceRow
                key={ws.id}
                ws={ws}
                onOpen={() => navigate(`/w/${ws.slug}`)}
                onSettings={() => navigate(`/w/${ws.slug}/settings`)}
              />
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
