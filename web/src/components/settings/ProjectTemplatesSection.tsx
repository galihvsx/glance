// Project templates management section for workspace settings (C17T1).
//
// Admin-only: the parent renders this only when the viewer is a workspace
// admin (role 20) — the backend rejects members/guests with 403, so there
// is no read-only mode to render. Matches the WebhooksSection pattern
// (hidden entirely for non-admins).
//
// A template is a blueprint applied when a project is created: states
// (replace the default seed), labels (workspace-scoped, existing names
// are reused), modules, and one default estimate scale. The payload
// editors below are plain dynamic row lists; the server validates the
// assembled document strictly on write.

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pencil, Plus, Trash2 } from "lucide-react";
import { ApiError } from "../../lib/api";
import {
  PROJECT_TEMPLATE_STATE_GROUPS,
  createProjectTemplate,
  deleteProjectTemplate,
  emptyProjectTemplatePayload,
  fetchProjectTemplates,
  projectTemplateKeys,
  summarizeProjectTemplatePayload,
  updateProjectTemplate,
  type ProjectTemplate,
  type ProjectTemplatePayload,
} from "../../lib/projectTemplates";
import { relativeTime } from "../../lib/relativeTime";
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
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../ui/dialog";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { Skeleton } from "../ui/skeleton";

function mutationError(e: unknown, fallback: string): string {
  return e instanceof ApiError ? e.message : fallback;
}

// --- row editors -------------------------------------------------------

function RowShell({
  children,
  onRemove,
  removeLabel,
}: {
  children: React.ReactNode;
  onRemove: () => void;
  removeLabel: string;
}) {
  return (
    <div className="flex items-end gap-2">
      <div className="grid flex-1 gap-2">{children}</div>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        aria-label={removeLabel}
        onClick={onRemove}
      >
        <Trash2 className="h-4 w-4" />
      </Button>
    </div>
  );
}

function StatesEditor({
  states,
  onChange,
}: {
  states: ProjectTemplatePayload["states"];
  onChange: (next: ProjectTemplatePayload["states"]) => void;
}) {
  return (
    <div className="space-y-2">
      {states.map((s, i) => (
        <RowShell
          key={i}
          removeLabel={`Remove state ${s.name || i + 1}`}
          onRemove={() => onChange(states.filter((_, j) => j !== i))}
        >
          <div className="grid grid-cols-[1fr_130px_90px] gap-2">
            <Input
              aria-label={`State ${i + 1} name`}
              placeholder="Name"
              value={s.name}
              onChange={(e) =>
                onChange(
                  states.map((x, j) =>
                    j === i ? { ...x, name: e.target.value } : x,
                  ),
                )
              }
            />
            <select
              aria-label={`State ${i + 1} group`}
              value={s.group}
              onChange={(e) =>
                onChange(
                  states.map((x, j) =>
                    j === i ? { ...x, group: e.target.value } : x,
                  ),
                )
              }
              className="rounded-md border border-input bg-background px-2 py-1 text-sm"
            >
              {PROJECT_TEMPLATE_STATE_GROUPS.map((g) => (
                <option key={g} value={g}>
                  {g}
                </option>
              ))}
            </select>
            <Input
              aria-label={`State ${i + 1} color`}
              type="color"
              value={/^#[0-9a-fA-F]{6}$/.test(s.color) ? s.color : "#6b7280"}
              onChange={(e) =>
                onChange(
                  states.map((x, j) =>
                    j === i ? { ...x, color: e.target.value } : x,
                  ),
                )
              }
              className="h-9 w-full cursor-pointer p-1"
            />
          </div>
        </RowShell>
      ))}
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() =>
          onChange([...states, { name: "", group: "backlog", color: "#6b7280" }])
        }
      >
        <Plus className="mr-1 h-4 w-4" /> Add state
      </Button>
    </div>
  );
}

function LabelsEditor({
  labels,
  onChange,
}: {
  labels: ProjectTemplatePayload["labels"];
  onChange: (next: ProjectTemplatePayload["labels"]) => void;
}) {
  return (
    <div className="space-y-2">
      {labels.map((l, i) => (
        <RowShell
          key={i}
          removeLabel={`Remove label ${l.name || i + 1}`}
          onRemove={() => onChange(labels.filter((_, j) => j !== i))}
        >
          <div className="grid grid-cols-[1fr_90px] gap-2">
            <Input
              aria-label={`Label ${i + 1} name`}
              placeholder="Name"
              value={l.name}
              onChange={(e) =>
                onChange(
                  labels.map((x, j) =>
                    j === i ? { ...x, name: e.target.value } : x,
                  ),
                )
              }
            />
            <Input
              aria-label={`Label ${i + 1} color`}
              type="color"
              value={/^#[0-9a-fA-F]{6}$/.test(l.color) ? l.color : "#6b7280"}
              onChange={(e) =>
                onChange(
                  labels.map((x, j) =>
                    j === i ? { ...x, color: e.target.value } : x,
                  ),
                )
              }
              className="h-9 w-full cursor-pointer p-1"
            />
          </div>
        </RowShell>
      ))}
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() => onChange([...labels, { name: "", color: "#6b7280" }])}
      >
        <Plus className="mr-1 h-4 w-4" /> Add label
      </Button>
    </div>
  );
}

function ModulesEditor({
  modules,
  onChange,
}: {
  modules: ProjectTemplatePayload["modules"];
  onChange: (next: ProjectTemplatePayload["modules"]) => void;
}) {
  return (
    <div className="space-y-2">
      {modules.map((m, i) => (
        <RowShell
          key={i}
          removeLabel={`Remove module ${m.name || i + 1}`}
          onRemove={() => onChange(modules.filter((_, j) => j !== i))}
        >
          <div className="grid grid-cols-2 gap-2">
            <Input
              aria-label={`Module ${i + 1} name`}
              placeholder="Name"
              value={m.name}
              onChange={(e) =>
                onChange(
                  modules.map((x, j) =>
                    j === i ? { ...x, name: e.target.value } : x,
                  ),
                )
              }
            />
            <Input
              aria-label={`Module ${i + 1} description`}
              placeholder="Description (optional)"
              value={m.description}
              onChange={(e) =>
                onChange(
                  modules.map((x, j) =>
                    j === i ? { ...x, description: e.target.value } : x,
                  ),
                )
              }
            />
          </div>
        </RowShell>
      ))}
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() => onChange([...modules, { name: "", description: "" }])}
      >
        <Plus className="mr-1 h-4 w-4" /> Add module
      </Button>
    </div>
  );
}

function EstimateEditor({
  scale,
  onChange,
}: {
  scale: NonNullable<ProjectTemplatePayload["estimate_scale"]> | null;
  onChange: (
    next: ProjectTemplatePayload["estimate_scale"],
  ) => void;
}) {
  return (
    <div className="space-y-2">
      <label className="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          checked={scale !== null}
          onChange={(e) =>
            onChange(
              e.target.checked
                ? { name: "", points: [] }
                : null,
            )
          }
        />
        Include a default estimate scale
      </label>
      {scale !== null && (
        <>
          <Input
            aria-label="Estimate scale name"
            placeholder="Scale name, e.g. Fibonacci"
            value={scale.name}
            onChange={(e) => onChange({ ...scale, name: e.target.value })}
          />
          {scale.points.map((p, i) => (
            <RowShell
              key={i}
              removeLabel={`Remove estimate point ${p.key || i + 1}`}
              onRemove={() =>
                onChange({
                  ...scale,
                  points: scale.points.filter((_, j) => j !== i),
                })
              }
            >
              <div className="grid grid-cols-[1fr_90px_1fr] gap-2">
                <Input
                  aria-label={`Estimate point ${i + 1} key`}
                  placeholder="Key"
                  value={p.key}
                  onChange={(e) =>
                    onChange({
                      ...scale,
                      points: scale.points.map((x, j) =>
                        j === i ? { ...x, key: e.target.value } : x,
                      ),
                    })
                  }
                />
                <Input
                  aria-label={`Estimate point ${i + 1} value`}
                  type="number"
                  placeholder="Value"
                  value={Number.isFinite(p.value) ? p.value : ""}
                  onChange={(e) =>
                    onChange({
                      ...scale,
                      points: scale.points.map((x, j) =>
                        j === i
                          ? { ...x, value: Math.trunc(Number(e.target.value)) || 0 }
                          : x,
                      ),
                    })
                  }
                />
                <Input
                  aria-label={`Estimate point ${i + 1} description`}
                  placeholder="Description (optional)"
                  value={p.description}
                  onChange={(e) =>
                    onChange({
                      ...scale,
                      points: scale.points.map((x, j) =>
                        j === i ? { ...x, description: e.target.value } : x,
                      ),
                    })
                  }
                />
              </div>
            </RowShell>
          ))}
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() =>
              onChange({
                ...scale,
                points: [
                  ...scale.points,
                  { key: "", value: 0, description: "" },
                ],
              })
            }
          >
            <Plus className="mr-1 h-4 w-4" /> Add point
          </Button>
        </>
      )}
    </div>
  );
}

// --- editor dialog -----------------------------------------------------

function TemplateDialog({
  slug,
  editing,
  onClose,
}: {
  slug: string;
  editing: ProjectTemplate | null; // null = create
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const [name, setName] = useState(editing?.name ?? "");
  const [description, setDescription] = useState(editing?.description ?? "");
  const [payload, setPayload] = useState<ProjectTemplatePayload>(
    () => editing?.payload ?? emptyProjectTemplatePayload(),
  );
  const [saveError, setSaveError] = useState<string | null>(null);

  const save = useMutation({
    mutationFn: () =>
      editing
        ? updateProjectTemplate(slug, editing.id, {
            name: name.trim(),
            description: description.trim(),
            payload,
          })
        : createProjectTemplate(slug, {
            name: name.trim(),
            description: description.trim(),
            payload,
          }),
    onSuccess: () => {
      void qc.invalidateQueries({
        queryKey: projectTemplateKeys(slug).templates,
      });
      onClose();
    },
    onError: (e) => setSaveError(mutationError(e, "Failed to save template")),
  });

  const patchPayload = (patch: Partial<ProjectTemplatePayload>) =>
    setPayload((p) => ({ ...p, ...patch }));

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>
            {editing ? "Edit project template" : "New project template"}
          </DialogTitle>
        </DialogHeader>
        <div className="space-y-5">
          <div className="grid gap-2 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor="pt-name">Name</Label>
              <Input
                id="pt-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="Web team"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="pt-desc">Description</Label>
              <Input
                id="pt-desc"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                placeholder="Optional"
              />
            </div>
          </div>

          <fieldset className="space-y-2">
            <legend className="text-sm font-medium">
              States{" "}
              <span className="font-normal text-muted-foreground">
                (replaces the default seed when non-empty)
              </span>
            </legend>
            <StatesEditor
              states={payload.states ?? []}
              onChange={(states) => patchPayload({ states })}
            />
          </fieldset>

          <fieldset className="space-y-2">
            <legend className="text-sm font-medium">
              Labels{" "}
              <span className="font-normal text-muted-foreground">
                (workspace-wide; existing names are reused)
              </span>
            </legend>
            <LabelsEditor
              labels={payload.labels ?? []}
              onChange={(labels) => patchPayload({ labels })}
            />
          </fieldset>

          <fieldset className="space-y-2">
            <legend className="text-sm font-medium">Modules</legend>
            <ModulesEditor
              modules={payload.modules ?? []}
              onChange={(modules) => patchPayload({ modules })}
            />
          </fieldset>

          <fieldset className="space-y-2">
            <legend className="text-sm font-medium">Estimate scale</legend>
            <EstimateEditor
              scale={payload.estimate_scale ?? null}
              onChange={(estimate_scale) => patchPayload({ estimate_scale })}
            />
          </fieldset>

          {saveError && (
            <Alert variant="destructive">
              <AlertDescription>{saveError}</AlertDescription>
            </Alert>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button
            onClick={() => {
              setSaveError(null);
              save.mutate();
            }}
            disabled={save.isPending || !name.trim()}
          >
            {save.isPending ? "Saving…" : editing ? "Save changes" : "Create template"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// --- section -----------------------------------------------------------

export default function ProjectTemplatesSection({ slug }: { slug: string }) {
  const qc = useQueryClient();
  const [dialog, setDialog] = useState<
    { open: boolean; editing: ProjectTemplate | null }
  >({ open: false, editing: null });
  const [error, setError] = useState<string | null>(null);

  const templatesQuery = useQuery({
    queryKey: projectTemplateKeys(slug).templates,
    queryFn: () => fetchProjectTemplates(slug),
  });

  const del = useMutation({
    mutationFn: (id: string) => deleteProjectTemplate(slug, id),
    onSuccess: () =>
      void qc.invalidateQueries({
        queryKey: projectTemplateKeys(slug).templates,
      }),
    onError: (e) => setError(mutationError(e, "Failed to delete template")),
  });

  function onDelete(t: ProjectTemplate) {
    // House confirm pattern (window.confirm, cf. Admin.tsx).
    if (
      !window.confirm(
        `Delete the project template "${t.name}"? Existing projects are unaffected.`,
      )
    )
      return;
    setError(null);
    del.mutate(t.id);
  }

  const templates = templatesQuery.data ?? null;

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0">
        <div>
          <CardTitle>Project templates</CardTitle>
          <CardDescription>
            Blueprints applied when a project is created: states, labels,
            modules and a default estimate scale.
          </CardDescription>
        </div>
        <Button
          size="sm"
          onClick={() => setDialog({ open: true, editing: null })}
        >
          <Plus className="mr-1 h-4 w-4" /> New template
        </Button>
      </CardHeader>
      <CardContent>
        {error && (
          <Alert variant="destructive" className="mb-4">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        {templates === null ? (
          <div className="space-y-2">
            <Skeleton className="h-14 w-full" />
            <Skeleton className="h-14 w-full" />
          </div>
        ) : templates.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No templates yet. Create one to reuse a project setup across
            new projects.
          </p>
        ) : (
          <ul className="divide-y divide-border">
            {templates.map((t) => (
              <li
                key={t.id}
                className="flex items-center justify-between gap-3 py-3"
              >
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium">{t.name}</p>
                  <p className="truncate text-xs text-muted-foreground">
                    {summarizeProjectTemplatePayload(t.payload)}
                    {" · "}
                    updated {relativeTime(t.updated_at)}
                  </p>
                  {t.description && (
                    <p className="truncate text-xs text-muted-foreground">
                      {t.description}
                    </p>
                  )}
                </div>
                <div className="flex shrink-0 items-center gap-1">
                  <Button
                    variant="ghost"
                    size="sm"
                    aria-label={`Edit template ${t.name}`}
                    onClick={() => setDialog({ open: true, editing: t })}
                  >
                    <Pencil className="h-4 w-4" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    aria-label={`Delete template ${t.name}`}
                    onClick={() => onDelete(t)}
                  >
                    <Trash2 className="h-4 w-4" />
                  </Button>
                </div>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
      {dialog.open && (
        <TemplateDialog
          slug={slug}
          editing={dialog.editing}
          onClose={() => setDialog({ open: false, editing: null })}
        />
      )}
    </Card>
  );
}
