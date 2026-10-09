// Issue templates management (C7T1): project settings CRUD for issue
// templates. Each template carries the issue defaults (template_data) the
// create-issue flow prefills from. Mutations are member (15)+; the server is
// the gate. Reuses the issue form field components (PriorityPicker,
// StatePicker, LabelPicker) plus EstimateSelect for the default values.

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pencil, Plus, Trash2 } from "lucide-react";
import { ApiError } from "../../lib/api";
import type { IssueState, Label as ProjectLabel } from "../../lib/types";
import {
  fetchEstimates,
  fetchLabels,
  fetchStates,
  type Estimate,
} from "../../lib/taxonomy";
import {
  createTemplate,
  defaultsToTemplateData,
  deleteTemplate,
  emptyDefaults,
  fetchTemplates,
  templateDataToDefaults,
  templateKeys,
  updateTemplate,
  type IssueTemplate,
  type TemplateDefaults,
} from "../../lib/templates";
import { relativeTime } from "../../lib/relativeTime";
import { Alert, AlertDescription } from "../ui/alert";
import { Button } from "../ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "../ui/card";
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
import { Textarea } from "../ui/textarea";
import PriorityPicker from "../issue/PriorityPicker";
import StatePicker from "../issue/StatePicker";
import LabelPicker from "../issue/LabelPicker";
import EstimateSelect from "../issue/EstimateSelect";

function errMsg(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message : fallback;
}

interface Draft {
  name: string;
  description: string;
  defaults: TemplateDefaults;
}

function draftFromTemplate(t: IssueTemplate): Draft {
  return {
    name: t.name,
    description: t.description ?? "",
    defaults: templateDataToDefaults(t.template_data),
  };
}

const newDraft = (): Draft => ({
  name: "",
  description: "",
  defaults: emptyDefaults(),
});

/** Default field values editor shared by create and edit. */
function DefaultsEditor({
  defaults,
  setDefaults,
  labels,
  states,
  estimates,
}: {
  defaults: TemplateDefaults;
  setDefaults: (d: TemplateDefaults) => void;
  labels: ProjectLabel[];
  states: IssueState[];
  estimates: Estimate[];
}) {
  const set = <K extends keyof TemplateDefaults>(k: K, v: TemplateDefaults[K]) =>
    setDefaults({ ...defaults, [k]: v });
  const appliedLabels = labels.filter((l) => defaults.labelIds.includes(l.id));
  return (
    <div className="space-y-4 rounded-md border p-3">
      <p className="text-sm font-medium">Default issue values</p>
      <div className="space-y-2">
        <Label htmlFor="tmpl-issue-name">Issue title</Label>
        <Input
          id="tmpl-issue-name"
          value={defaults.issueName}
          onChange={(e) => set("issueName", e.target.value)}
          placeholder="e.g. Fix the login redirect"
        />
      </div>
      <div className="space-y-2">
        <Label htmlFor="tmpl-issue-desc">Issue description</Label>
        <Textarea
          id="tmpl-issue-desc"
          value={defaults.issueDescription}
          onChange={(e) => set("issueDescription", e.target.value)}
          placeholder="What needs to happen…"
          rows={3}
        />
      </div>
      <div className="flex flex-wrap gap-4">
        <div className="space-y-2">
          <Label>Priority</Label>
          <div className="flex items-center gap-2">
            <PriorityPicker
              value={defaults.priority ?? 0}
              onChange={(v) => set("priority", v)}
            />
            {defaults.priority !== null && (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => set("priority", null)}
              >
                Clear
              </Button>
            )}
          </div>
          <p className="text-xs text-muted-foreground">
            {defaults.priority === null
              ? "No default — the create form keeps its own."
              : "Applied when this template is picked."}
          </p>
        </div>
        {states.length > 0 && (
          <div className="space-y-2">
            <Label>State</Label>
            <div className="flex items-center gap-2">
              <StatePicker
                states={states}
                value={defaults.stateId ?? ""}
                onChange={(v) => set("stateId", v || null)}
              />
              {defaults.stateId !== null && (
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => set("stateId", null)}
                >
                  Clear
                </Button>
              )}
            </div>
          </div>
        )}
        <div className="space-y-2">
          <Label>Estimate</Label>
          <div className="flex items-center gap-2">
            <EstimateSelect
              estimates={estimates}
              value={defaults.estimatePointId ?? ""}
              onChange={(v) => set("estimatePointId", v || null)}
            />
            {defaults.estimatePointId !== null && (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => set("estimatePointId", null)}
              >
                Clear
              </Button>
            )}
          </div>
        </div>
        <div className="space-y-2">
          <Label>Labels</Label>
          <div>
            <LabelPicker
              labels={labels}
              applied={appliedLabels}
              onToggle={(id, currentlyApplied) =>
                set(
                  "labelIds",
                  currentlyApplied
                    ? defaults.labelIds.filter((x) => x !== id)
                    : [...defaults.labelIds, id],
                )
              }
            />
          </div>
          <p className="text-xs text-muted-foreground">
            Labels can't be set at issue creation — they're recorded here and
            applied by the template apply endpoint.
          </p>
        </div>
      </div>
    </div>
  );
}

export default function TemplatesSection({
  slug,
  identifier,
  canEdit,
}: {
  slug: string;
  identifier: string;
  canEdit: boolean;
}) {
  const queryClient = useQueryClient();
  const keys = templateKeys(slug, identifier);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [editing, setEditing] = useState<IssueTemplate | null>(null);
  const [error, setError] = useState<string | null>(null);

  const templatesQuery = useQuery({
    queryKey: keys.templates,
    queryFn: () => fetchTemplates(slug, identifier),
  });

  const modalOpen = draft !== null || editing !== null;
  const labelsQuery = useQuery({
    queryKey: ["taxonomy", `${slug}/${identifier}`, "labels"],
    queryFn: () => fetchLabels(slug, identifier),
    enabled: modalOpen,
  });
  const statesQuery = useQuery({
    queryKey: ["taxonomy", `${slug}/${identifier}`, "states"],
    queryFn: () => fetchStates(slug, identifier),
    enabled: modalOpen,
  });
  const estimatesQuery = useQuery({
    queryKey: ["taxonomy", `${slug}/${identifier}`, "estimates"],
    queryFn: () => fetchEstimates(slug, identifier),
    enabled: modalOpen,
  });

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: keys.templates });

  const saveMutation = useMutation({
    mutationFn: () => {
      if (!draft) throw new Error("no draft");
      const desc = draft.description.trim();
      const body = {
        name: draft.name.trim(),
        description: desc ? desc : "",
        template_data: defaultsToTemplateData(draft.defaults),
      };
      return editing
        ? updateTemplate(slug, identifier, editing.id, body)
        : createTemplate(slug, identifier, body);
    },
    onSuccess: () => {
      setDraft(null);
      setEditing(null);
      setError(null);
      void invalidate();
    },
    onError: (e) => setError(errMsg(e, "Failed to save template")),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteTemplate(slug, identifier, id),
    onSuccess: () => {
      setError(null);
      void invalidate();
    },
    onError: (e) => setError(errMsg(e, "Failed to delete template")),
  });

  function onDelete(t: IssueTemplate) {
    if (!window.confirm(`Delete template "${t.name}"? This cannot be undone.`))
      return;
    deleteMutation.mutate(t.id);
  }

  function startEdit(t: IssueTemplate) {
    setEditing(t);
    setDraft(draftFromTemplate(t));
    setError(null);
  }

  const templates = templatesQuery.data ?? [];
  // StatePicker / LabelPicker want the full API row shapes; the taxonomy
  // client returns the settings subset — pad the audit timestamps.
  const states: IssueState[] = (statesQuery.data ?? []).map((s) => ({
    ...s,
    created_at: "",
    updated_at: "",
  }));
  const labels: ProjectLabel[] = (labelsQuery.data ?? []).map((l) => ({
    ...l,
    parent_id: null,
    created_at: "",
    updated_at: "",
  }));
  const estimates: Estimate[] = estimatesQuery.data ?? [];

  // Human-readable summary of one template's defaults for the list row.
  function defaultsSummary(t: IssueTemplate): string {
    const d = templateDataToDefaults(t.template_data);
    const parts: string[] = [];
    if (d.issueName) parts.push(`title: ${d.issueName}`);
    if (d.priority !== null) parts.push(`priority ${d.priority}`);
    if (d.stateId) {
      const s = statesQuery.data?.find((x) => x.id === d.stateId);
      parts.push(`state: ${s?.name ?? "?"}`);
    }
    if (d.estimatePointId) parts.push("estimate set");
    if (d.labelIds.length > 0)
      parts.push(
        `${d.labelIds.length} label${d.labelIds.length > 1 ? "s" : ""}`,
      );
    return parts.join(" · ");
  }

  const modalTitle = editing ? `Edit template` : "New template";

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle>Templates</CardTitle>
        {canEdit && (
          <Button
            size="sm"
            onClick={() => {
              setEditing(null);
              setDraft(newDraft());
              setError(null);
            }}
          >
            <Plus className="mr-1 h-4 w-4" /> New template
          </Button>
        )}
      </CardHeader>
      <CardContent className="space-y-3">
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        {!canEdit && (
          <p className="text-sm text-muted-foreground">
            You are a guest here — templates are read-only. Members can manage
            them.
          </p>
        )}
        {templatesQuery.isLoading ? (
          <Skeleton className="h-10 w-full" />
        ) : templatesQuery.isError ? (
          <Alert variant="destructive">
            <AlertDescription>
              Failed to load templates.
              <Button
                variant="link"
                size="sm"
                onClick={() => void templatesQuery.refetch()}
              >
                Retry
              </Button>
            </AlertDescription>
          </Alert>
        ) : templates.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No templates yet. Create one to prefill the create-issue form with
            one click.
          </p>
        ) : (
          <ul className="divide-y rounded-md border">
            {templates.map((t) => (
              <li
                key={t.id}
                className="flex items-center gap-3 px-3 py-2"
              >
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm font-medium">{t.name}</p>
                  {t.description && (
                    <p className="truncate text-xs text-muted-foreground">
                      {t.description}
                    </p>
                  )}
                  {defaultsSummary(t) && (
                    <p className="truncate text-xs text-muted-foreground">
                      {defaultsSummary(t)}
                    </p>
                  )}
                  <p className="text-xs text-muted-foreground">
                    Created {relativeTime(t.created_at)}
                  </p>
                </div>
                {canEdit && (
                  <div className="flex shrink-0 gap-1">
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={`Edit ${t.name}`}
                      onClick={() => startEdit(t)}
                    >
                      <Pencil className="h-4 w-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={`Delete ${t.name}`}
                      onClick={() => onDelete(t)}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </div>
                )}
              </li>
            ))}
          </ul>
        )}

        {/* Create / edit dialog */}
        <Dialog
          open={modalOpen}
          onOpenChange={(o) => {
            if (!o) {
              setDraft(null);
              setEditing(null);
            }
          }}
        >
          <DialogContent className="max-w-2xl">
            <DialogHeader>
              <DialogTitle>{modalTitle}</DialogTitle>
            </DialogHeader>
            {draft && (
              <div className="max-h-[70vh] space-y-4 overflow-y-auto pr-1">
                <div className="space-y-2">
                  <Label htmlFor="tmpl-name">Template name</Label>
                  <Input
                    id="tmpl-name"
                    value={draft.name}
                    onChange={(e) =>
                      setDraft({ ...draft, name: e.target.value })
                    }
                    placeholder="Bug report"
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="tmpl-desc">
                    Description{" "}
                    <span className="font-normal text-muted-foreground">
                      (shown in the template list)
                    </span>
                  </Label>
                  <Input
                    id="tmpl-desc"
                    value={draft.description}
                    onChange={(e) =>
                      setDraft({ ...draft, description: e.target.value })
                    }
                    placeholder="What this template is for"
                  />
                </div>
                {labelsQuery.isLoading ||
                statesQuery.isLoading ||
                estimatesQuery.isLoading ? (
                  <Skeleton className="h-24 w-full" />
                ) : (
                  <DefaultsEditor
                    defaults={draft.defaults}
                    setDefaults={(d) => setDraft({ ...draft, defaults: d })}
                    labels={labels}
                    states={states}
                    estimates={estimates}
                  />
                )}
              </div>
            )}
            <DialogFooter>
              <Button
                disabled={
                  !draft ||
                  !draft.name.trim() ||
                  saveMutation.isPending ||
                  labelsQuery.isLoading ||
                  statesQuery.isLoading ||
                  estimatesQuery.isLoading
                }
                onClick={() => saveMutation.mutate()}
              >
                {saveMutation.isPending
                  ? "Saving…"
                  : editing
                    ? "Save changes"
                    : "Create template"}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </CardContent>
    </Card>
  );
}
