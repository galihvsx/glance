// Workspace project picker (C19T9 polish): dense 47px project rows in
// the token ramp — project mark, name 13px/600, identifier chip, chevron.
// Shared PageHeader breadcrumb replaces the per-page h1; "New project"
// moves to the header's primary button, My work / Settings to header
// controls. Archived projects get the 12px/600 hairline group bar.
import { useCallback, useEffect, useState, type FormEvent } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { ChevronRight, Layers, Settings, User } from "lucide-react";
import { api, ApiError } from "../lib/api";
import type { Project } from "../lib/types";
import {
  fetchProjectTemplates,
  summarizeProjectTemplatePayload,
  type ProjectTemplate,
} from "../lib/projectTemplates";
import PageHeader, { pageHeaderControlClass } from "../components/shell/PageHeader";
import EmptyState from "../components/personal/EmptyState";
import { SectionBar } from "../components/personal/SectionBar";
import { Button } from "../components/ui/button";
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
import { Skeleton } from "../components/ui/skeleton";
import { Alert, AlertDescription } from "../components/ui/alert";

function ProjectMark({ project }: { project: Project }) {
  if (project.icon) {
    return (
      <span className="text-base leading-none" aria-hidden="true">
        {project.icon}
      </span>
    );
  }
  return (
    <span
      className="flex h-5 w-5 shrink-0 items-center justify-center rounded bg-raised text-[9px] font-bold text-ink-secondary"
      aria-hidden="true"
    >
      {project.identifier.charAt(0)}
    </span>
  );
}

function ProjectRow({
  project,
  slug,
  dimmed,
}: {
  project: Project;
  slug: string;
  dimmed?: boolean;
}) {
  const navigate = useNavigate();
  return (
    <li>
      <button
        type="button"
        onClick={() => navigate(`/w/${slug}/p/${project.identifier}`)}
        aria-label={`Open project ${project.name}`}
        className={`flex min-h-[47px] w-full items-center gap-3 px-3 py-2 text-left transition-colors hover:bg-wash-hover ${
          dimmed ? "opacity-70" : ""
        }`}
      >
        <ProjectMark project={project} />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-[13px] font-semibold text-ink">
            {project.name}
          </span>
        </span>
        {project.archived_at && (
          <span className="shrink-0 text-[10px] text-ink-muted">
            archived {new Date(project.archived_at).toLocaleDateString()}
          </span>
        )}
        <span className="shrink-0 rounded border border-border px-1.5 py-0.5 font-mono text-[10px] leading-none text-ink-secondary">
          {project.identifier}
        </span>
        <ChevronRight
          className="h-3.5 w-3.5 shrink-0 text-ink-muted"
          aria-hidden="true"
        />
      </button>
    </li>
  );
}

export default function Projects() {
  const { slug } = useParams<{ slug: string }>();
  const navigate = useNavigate();
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  // C18T3: archived projects live in their own section below (the list
  // endpoint excludes them by default; ?archived=true includes them).
  const [archived, setArchived] = useState<Project[] | null>(null);
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
    // Archived projects: separate section, never mixed into the live list.
    try {
      const data = await api.get<{ projects: Project[] }>(
        `/api/v1/workspaces/${encodeURIComponent(slug)}/projects?archived=true`,
      );
      setArchived(data.projects.filter((p) => p.archived_at));
    } catch {
      setArchived([]);
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
    <div className="w-full">
      {/* C19T4-style shared header; the New-project dialog opens from the
          primary button. */}
      <PageHeader
        viewName="Projects"
        count={projects?.length ?? 0}
        addButton={{ label: "New project", onClick: () => setDialogOpen(true) }}
        showAnalytics={false}
        extraControls={
          <>
            <button
              type="button"
              onClick={() => navigate(`/w/${slug}/my-work`)}
              className={pageHeaderControlClass}
            >
              <User className="h-[13px] w-[13px]" aria-hidden="true" />
              My work
            </button>
            <button
              type="button"
              onClick={() => navigate(`/w/${slug}/settings`)}
              className={pageHeaderControlClass}
            >
              <Settings className="h-[13px] w-[13px]" aria-hidden="true" />
              Settings
            </button>
          </>
        }
      />

      {/* The create dialog lives at page level so the header button and
          the empty state can both open it. */}
      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
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
              <p className="text-xs text-ink-muted">
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
                <p className="text-xs text-ink-muted">
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

      <div className="mx-auto w-full max-w-3xl space-y-4 p-6">
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}

        {projects === null ? (
          <div className="space-y-2">
            <Skeleton className="h-[47px] w-full" />
            <Skeleton className="h-[47px] w-full" />
            <Skeleton className="h-[47px] w-full" />
          </div>
        ) : projects.length === 0 ? (
          <EmptyState icon={Layers} title="No projects yet">
            <p>Create the first project in this workspace.</p>
            <Button
              variant="outline"
              size="sm"
              className="mt-3"
              onClick={() => setDialogOpen(true)}
            >
              New project
            </Button>
          </EmptyState>
        ) : (
          <ul className="divide-y divide-border-hairline overflow-hidden rounded-md border border-border">
            {projects.map((p) => (
              <ProjectRow key={p.id} project={p} slug={slug ?? ""} />
            ))}
          </ul>
        )}

        {/* ---------- Archived (C18T3) ---------- */}
        {archived !== null && archived.length > 0 && (
          <div className="pt-4">
            <SectionBar label="Archived" count={archived.length} />
            <p className="px-3 py-2 text-xs text-ink-muted">
              Hidden from the sidebar and search. Admins can restore a project
              from its settings → Danger zone.
            </p>
            <ul className="mt-2 divide-y divide-border-hairline overflow-hidden rounded-md border border-border">
              {archived.map((p) => (
                <ProjectRow
                  key={p.id}
                  project={p}
                  slug={slug ?? ""}
                  dimmed
                />
              ))}
            </ul>
          </div>
        )}
      </div>
    </div>
  );
}
