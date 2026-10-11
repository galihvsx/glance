import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, ApiError } from "../lib/api";
import type { Project } from "../lib/types";
import {
  fetchProjectTemplates,
  summarizeProjectTemplatePayload,
  type ProjectTemplate,
} from "../lib/projectTemplates";
import { Button } from "../components/ui/button";
import {
  Card,
  CardDescription,
  CardHeader,
  CardTitle,
} from "../components/ui/card";
import EmojiPicker from "../components/project/EmojiPicker";
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

export default function Projects() {
  const { slug } = useParams<{ slug: string }>();
  const navigate = useNavigate();
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [name, setName] = useState("");
  const [identifier, setIdentifier] = useState("");
  // C18T4: optional project emoji icon picked in the dialog.
  const [icon, setIcon] = useState<string | null>(null);
  // C17T1: optional project template applied in the creation transaction.
  const [templates, setTemplates] = useState<ProjectTemplate[] | null>(null);
  const [templateId, setTemplateId] = useState("");
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

  // Load the workspace's project templates when the dialog opens so the
  // picker can offer them. Guests can't create projects anyway; a failed
  // fetch just hides the picker (never blocks creation).
  useEffect(() => {
    if (!dialogOpen || !slug) return;
    setTemplates(null);
    setTemplateId("");
    setIcon(null);
    fetchProjectTemplates(slug)
      .then(setTemplates)
      .catch(() => setTemplates([]));
  }, [dialogOpen, slug]);

  async function onCreate(e: FormEvent) {
    e.preventDefault();
    setFormError(null);
    setCreating(true);
    try {
      // Identifier is uppercased server-side too; we mirror it in the input.
      const p = await api.post<Project>(
        `/api/v1/workspaces/${encodeURIComponent(slug ?? "")}/projects`,
        {
          name: name.trim(),
          identifier: identifier.trim(),
          // C18T4: optional emoji icon; omitted when unset.
          ...(icon ? { icon } : {}),
          // C17T1: template seeds states/labels/modules/estimate scale
          // in the same transaction as project creation.
          ...(templateId ? { template_id: templateId } : {}),
        },
      );
      setDialogOpen(false);
      setName("");
      setIdentifier("");
      setTemplateId("");
      setIcon(null);
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
            to="/w"
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
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => navigate(`/w/${slug}/my-work`)}
          >
            My work
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => navigate(`/w/${slug}/settings`)}
          >
            Settings
          </Button>
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
              <div className="flex items-end gap-3">
                <div className="space-y-2">
                  <Label>Icon</Label>
                  <EmojiPicker
                    value={icon}
                    onChange={setIcon}
                    label="Choose project icon"
                  />
                </div>
                <div className="flex-1 space-y-2">
                  <Label htmlFor="p-name">Name</Label>
                  <Input
                    id="p-name"
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                    placeholder="Platform"
                    required
                  />
                </div>
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
              {templates !== null && templates.length > 0 && (
                <div className="space-y-2">
                  <Label htmlFor="p-template">Template</Label>
                  <select
                    id="p-template"
                    value={templateId}
                    onChange={(e) => setTemplateId(e.target.value)}
                    className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
                  >
                    <option value="">
                      No template — default states
                    </option>
                    {templates.map((t) => (
                      <option key={t.id} value={t.id}>
                        {t.name} — {summarizeProjectTemplatePayload(t.payload)}
                      </option>
                    ))}
                  </select>
                  <p className="text-xs text-muted-foreground">
                    Applies the template&apos;s states, labels, modules and
                    estimate scale when the project is created.
                  </p>
                </div>
              )}
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
            <ActionCard
              key={p.id}
              role="link"
              label={`Open project ${p.name}`}
              onActivate={() => navigate(`/w/${slug}/p/${p.identifier}`)}
            >
              <CardHeader className="flex flex-row items-center gap-3 space-y-0">
                {p.icon ? (
                  <span className="text-xl leading-none" aria-hidden="true">
                    {p.icon}
                  </span>
                ) : null}
                <Badge>{p.identifier}</Badge>
                <CardTitle className="text-base">{p.name}</CardTitle>
              </CardHeader>
            </ActionCard>
          ))}
        </div>
      )}
    </div>
  );
}
