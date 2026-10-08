import { useCallback, useEffect, useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { api, ApiError } from "../lib/api";
import type { Workspace } from "../lib/types";
import { roleLabel } from "../lib/types";
import { Button } from "../components/ui/button";
import {
  Card,
  CardDescription,
  CardHeader,
  CardTitle,
} from "../components/ui/card";
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
import { Badge } from "../components/ui/badge";
import { ActionCard } from "../components/ui/action-card";
import { Skeleton } from "../components/ui/skeleton";
import { Alert, AlertDescription } from "../components/ui/alert";
import ThemeToggle from "../components/ThemeToggle";

function slugify(name: string): string {
  return name
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
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
    <div className="mx-auto w-full max-w-3xl p-6">
      <div className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Workspaces</h1>
          <p className="text-sm text-muted-foreground">
            Pick a workspace to see its projects.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <ThemeToggle />
          <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
            <Button onClick={() => setDialogOpen(true)}>New workspace</Button>
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
                <p className="text-xs text-muted-foreground">
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
        </div>
      </div>

      {error && (
        <Alert variant="destructive" className="mb-4">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {workspaces === null ? (
        <div className="space-y-3">
          <Skeleton className="h-20 w-full" />
          <Skeleton className="h-20 w-full" />
          <Skeleton className="h-20 w-full" />
        </div>
      ) : workspaces.length === 0 ? (
        <Card>
          <CardHeader>
            <CardTitle>No workspaces yet</CardTitle>
            <CardDescription>
              Create your first workspace to start tracking issues.
            </CardDescription>
          </CardHeader>
        </Card>
      ) : (
        <div className="space-y-3">
          {workspaces.map((ws) => (
            <ActionCard
              key={ws.id}
              role="link"
              label={`Open workspace ${ws.name}`}
              onActivate={() => navigate(`/w/${ws.slug}`)}
            >
              <CardHeader className="flex flex-row items-center justify-between space-y-0">
                <div>
                  <CardTitle className="text-base">{ws.name}</CardTitle>
                  <CardDescription>/{ws.slug}</CardDescription>
                </div>
                <Badge variant="secondary">{roleLabel(ws.role)}</Badge>
              </CardHeader>
            </ActionCard>
          ))}
        </div>
      )}
    </div>
  );
}
