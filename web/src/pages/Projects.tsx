import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, ApiError } from "../lib/api";
import type { Project } from "../lib/types";
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
import { Skeleton } from "../components/ui/skeleton";
import { Alert, AlertDescription } from "../components/ui/alert";

export default function Projects() {
  const { slug } = useParams<{ slug: string }>();
  const navigate = useNavigate();
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [name, setName] = useState("");
  const [identifier, setIdentifier] = useState("");
  const [formError, setFormError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  const load = useCallback(async () => {
    if (!slug) return;
    setError(null);
    try {
      // Wrapped shape: {"projects": [...]}
      const data = await api.get<{ projects: Project[] }>(
        `/api/v1/workspaces/${encodeURIComponent(slug)}/projects`,
      );
      setProjects(data.projects);
    } catch (e) {
      setProjects([]);
      setError(e instanceof ApiError ? e.message : "Failed to load projects");
    }
  }, [slug]);

  useEffect(() => {
    void load();
  }, [load]);

  async function onCreate(e: FormEvent) {
    e.preventDefault();
    setFormError(null);
    setCreating(true);
    try {
      // Identifier is uppercased server-side too; we mirror it in the input.
      const p = await api.post<Project>(
        `/api/v1/workspaces/${encodeURIComponent(slug ?? "")}/projects`,
        { name: name.trim(), identifier: identifier.trim() },
      );
      setDialogOpen(false);
      setName("");
      setIdentifier("");
      navigate(`/w/${slug}/p/${p.identifier}`);
    } catch (err) {
      // 409 duplicate identifier surfaces the API's message verbatim.
      setFormError(
        err instanceof ApiError ? err.message : "Failed to create project",
      );
    } finally {
      setCreating(false);
    }
  }

  return (
    <div className="mx-auto w-full max-w-3xl p-6">
      <div className="mb-6 flex items-center justify-between">
        <div>
          <Link
            to="/"
            className="text-xs text-muted-foreground hover:underline"
          >
            ← Workspaces
          </Link>
          <h1 className="text-2xl font-semibold tracking-tight">
            Projects in /{slug}
          </h1>
          <p className="text-sm text-muted-foreground">
            Pick a project to see its issues and states.
          </p>
        </div>
        <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
          <Button onClick={() => setDialogOpen(true)}>New project</Button>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>New project</DialogTitle>
              <DialogDescription>
                Projects get their own issue sequences, e.g. ENG-123.
              </DialogDescription>
            </DialogHeader>
            <form onSubmit={onCreate} className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="p-name">Name</Label>
                <Input
                  id="p-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="Platform"
                  required
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="p-identifier">Identifier</Label>
                <Input
                  id="p-identifier"
                  value={identifier}
                  onChange={(e) =>
                    setIdentifier(e.target.value.toUpperCase())
                  }
                  placeholder="ENG"
                  maxLength={12}
                  required
                />
                <p className="text-xs text-muted-foreground">
                  Up to 12 letters and digits. Issues are numbered ENG-1,
                  ENG-2, …
                </p>
              </div>
              {formError && (
                <Alert variant="destructive">
                  <AlertDescription>{formError}</AlertDescription>
                </Alert>
              )}
              <DialogFooter>
                <Button
                  type="submit"
                  disabled={creating || !name || !identifier}
                >
                  {creating ? "Creating…" : "Create project"}
                </Button>
              </DialogFooter>
            </form>
          </DialogContent>
        </Dialog>
      </div>

      {error && (
        <Alert variant="destructive" className="mb-4">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {projects === null ? (
        <div className="space-y-3">
          <Skeleton className="h-20 w-full" />
          <Skeleton className="h-20 w-full" />
          <Skeleton className="h-20 w-full" />
        </div>
      ) : projects.length === 0 ? (
        <Card>
          <CardHeader>
            <CardTitle>No projects yet</CardTitle>
            <CardDescription>
              Create the first project in this workspace.
            </CardDescription>
          </CardHeader>
        </Card>
      ) : (
        <div className="space-y-3">
          {projects.map((p) => (
            <Card
              key={p.id}
              className="cursor-pointer transition-colors hover:bg-accent"
              onClick={() => navigate(`/w/${slug}/p/${p.identifier}`)}
            >
              <CardHeader className="flex flex-row items-center gap-3 space-y-0">
                <Badge>{p.identifier}</Badge>
                <CardTitle className="text-base">{p.name}</CardTitle>
              </CardHeader>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}
