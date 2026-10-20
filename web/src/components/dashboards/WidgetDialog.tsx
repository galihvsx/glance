// C81T1: create/edit widget dialog — the six widget types with live
// preview icons, title, project multi-select (default: all visible
// projects), group-by select (hidden for `number`, which shows a
// filter summary instead), and assignee/priority/state/label filter
// pickers reusing FilterPanel's CheckRow language via FilterPicker.
// Config keys mirror the backend's validated schema (see
// internal/service/dashboard.go parseWidgetConfig): group_by is
// required for chart types and meaningless for number; filters carry
// UUID arrays; project_ids [] = every live workspace project.
import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../../lib/api";
import type {
  IssueState,
  Label,
  Member,
  Project,
} from "../../lib/types";
import {
  EMPTY_WIDGET_FILTERS,
  GROUP_BY_OPTIONS,
  WIDGET_TYPE_LABELS,
  WIDGET_TYPES,
  filterSummary,
  isChartType,
  normalizeWidgetConfig,
  type DashboardWidget,
  type WidgetConfig,
  type WidgetFilters,
  type WidgetGroupBy,
  type WidgetType,
} from "../../lib/dashboards";
import { PRIORITY_LABELS } from "../../lib/types";
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
import { Label as UiLabel } from "../ui/label";
import { NativeSelect } from "../ui/native-select";
import { Alert, AlertDescription } from "../ui/alert";
import { cn } from "cn";
import FilterPicker, { type FilterPickerOption } from "./FilterPicker";

export interface WidgetFormValue {
  title: string;
  widget_type: WidgetType;
  config: WidgetConfig;
}

/** Mini SVG preview per widget type (the dialog's type picker). */
function TypeGlyph({ type }: { type: WidgetType }) {
  const cls = "h-9 w-full";
  const bar = "fill-primary/70";
  const fg = "stroke-primary";
  switch (type) {
    case "bar":
      return (
        <svg viewBox="0 0 48 24" className={cls} aria-hidden="true">
          <rect x="6" y="12" width="8" height="9" rx="1" className={bar} />
          <rect x="20" y="6" width="8" height="15" rx="1" className={bar} />
          <rect x="34" y="2" width="8" height="19" rx="1" className={bar} />
        </svg>
      );
    case "line":
      return (
        <svg viewBox="0 0 48 24" className={cls} aria-hidden="true">
          <polyline
            points="4,18 16,10 28,13 44,4"
            fill="none"
            strokeWidth="2.5"
            strokeLinecap="round"
            className={fg}
          />
          <circle cx="4" cy="18" r="2" className={bar} />
          <circle cx="44" cy="4" r="2" className={bar} />
        </svg>
      );
    case "area":
      return (
        <svg viewBox="0 0 48 24" className={cls} aria-hidden="true">
          <path
            d="M2,19 L14,10 L26,14 L40,5 L46,8 L46,22 L2,22 Z"
            className="fill-primary/25"
          />
          <polyline
            points="2,19 14,10 26,14 40,5 46,8"
            fill="none"
            strokeWidth="2"
            strokeLinecap="round"
            className={fg}
          />
        </svg>
      );
    case "donut":
      return (
        <svg viewBox="0 0 24 24" className={cn(cls, "mx-auto w-9")} aria-hidden="true">
          <circle cx="12" cy="12" r="9" fill="none" strokeWidth="5" className="stroke-primary/70" strokeDasharray="28 56.5" strokeDashoffset="8" />
          <circle cx="12" cy="12" r="9" fill="none" strokeWidth="5" className="stroke-primary/25" strokeDasharray="28.5 56.5" strokeDashoffset="-20" />
        </svg>
      );
    case "pie":
      return (
        <svg viewBox="0 0 24 24" className={cn(cls, "mx-auto w-9")} aria-hidden="true">
          <path d="M12,12 L12,3 A9,9 0 0,1 20.5,16.5 Z" className="fill-primary/80" />
          <path d="M12,12 L20.5,16.5 A9,9 0 0,1 3.5,17.5 Z" className="fill-primary/45" />
          <path d="M12,12 L3.5,17.5 A9,9 0 0,1 12,3 Z" className="fill-primary/20" />
        </svg>
      );
    case "number":
      return (
        <div className={cn(cls, "flex items-center justify-center")}>
          <span className="text-2xl font-bold tabular-nums text-primary">42</span>
        </div>
      );
  }
}

interface StateOption extends FilterPickerOption {
  projectId: string;
}
interface LabelOption extends FilterPickerOption {
  projectId: string;
}

export default function WidgetDialog({
  open,
  onOpenChange,
  mode,
  slug,
  initial,
  pending,
  serverError,
  onSubmit,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  mode: "create" | "edit";
  slug: string;
  initial?: DashboardWidget | null;
  pending?: boolean;
  serverError?: string | null;
  onSubmit: (v: WidgetFormValue) => void;
}) {
  const wsBase = `/api/v1/workspaces/${encodeURIComponent(slug)}`;

  const [title, setTitle] = useState("");
  const [widgetType, setWidgetType] = useState<WidgetType>("bar");
  const [groupBy, setGroupBy] = useState<WidgetGroupBy>("state");
  const [projectIds, setProjectIds] = useState<string[]>([]);
  const [filters, setFilters] = useState<WidgetFilters>(EMPTY_WIDGET_FILTERS);

  useEffect(() => {
    if (open) {
      const cfg = normalizeWidgetConfig(initial?.config ?? {});
      setTitle(initial?.title ?? "");
      setWidgetType(initial?.widget_type ?? "bar");
      setGroupBy(cfg.group_by ?? "state");
      setProjectIds(initial?.config?.project_ids ?? []);
      setFilters(cfg.filters);
    }
  }, [open, initial?.title, initial?.widget_type, initial?.config]);

  // Reset the form every open (create after edit must not leak state).

  const projectsQuery = useQuery({
    queryKey: ["dashboard-projects", slug],
    queryFn: () =>
      api
        .get<{ projects: Project[] }>(`${wsBase}/projects`)
        .then((d) => d.projects.filter((p) => !p.archived_at)),
    enabled: open,
  });
  const membersQuery = useQuery({
    queryKey: ["members", slug],
    queryFn: () =>
      api
        .get<{ members: Member[] }>(`${wsBase}/members`)
        .then((d) => d.members),
    enabled: open,
  });

  const projects = useMemo(
    () => projectsQuery.data ?? [],
    [projectsQuery.data],
  );
  const scopeProjects =
    projectIds.length > 0
      ? projects.filter((p) => projectIds.includes(p.id))
      : projects;
  const scopeKey = [...scopeProjects.map((p) => p.id)].sort().join(",");

  // State/label options are unioned across the in-scope projects (state
  // ids are per-project; the backend ANDs them against the same scope,
  // so unioning is the honest picker).
  const statesQuery = useQuery({
    queryKey: ["dashboard-states", slug, scopeKey],
    queryFn: async (): Promise<StateOption[]> => {
      const per = await Promise.all(
        scopeProjects.map((p) =>
          api
            .get<{ states: IssueState[] }>(
              `${wsBase}/projects/${encodeURIComponent(p.identifier)}/states`,
            )
            .then((d) =>
              d.states.map((s) => ({
                id: s.id,
                label: s.name,
                hint: p.name,
                projectId: p.id,
                badge: (
                  <span
                    className="size-2.5 shrink-0 rounded-full"
                    style={{ backgroundColor: s.color }}
                  />
                ),
              })),
            ),
        ),
      );
      return per.flat();
    },
    enabled: open && projectsQuery.isFetched,
  });
  const labelsQuery = useQuery({
    queryKey: ["dashboard-labels", slug, scopeKey],
    queryFn: async (): Promise<LabelOption[]> => {
      const per = await Promise.all(
        scopeProjects.map((p) =>
          api
            .get<{ labels: Label[] }>(
              `${wsBase}/projects/${encodeURIComponent(p.identifier)}/labels`,
            )
            .then((d) =>
              d.labels.map((l) => ({
                id: l.id,
                label: l.name,
                hint: p.name,
                projectId: p.id,
                badge: (
                  <span
                    className="size-2.5 shrink-0 rounded-full"
                    style={{ backgroundColor: l.color }}
                  />
                ),
              })),
            ),
        ),
      );
      return per.flat();
    },
    enabled: open && projectsQuery.isFetched,
  });

  // When the project scope changes, drop filter selections whose
  // options are no longer in scope (state/label ids are per-project).
  const inScope = new Set([
    ...scopeProjects.map((p) => p.id),
    ...(statesQuery.data ?? []).map((s) => s.id),
    ...(labelsQuery.data ?? []).map((l) => l.id),
  ]);
  const prunedFilters = useMemo(() => {
    return {
      assignees: filters.assignees,
      priorities: filters.priorities,
      states: filters.states.filter((id) => inScope.has(id)),
      labels: filters.labels.filter((id) => inScope.has(id)),
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scopeKey, statesQuery.data, labelsQuery.data]);

  const toggle = (key: "assignees" | "states" | "labels", id: string) => {
    const list = prunedFilters[key];
    const next = list.includes(id)
      ? list.filter((x) => x !== id)
      : [...list, id];
    setFilters({ ...prunedFilters, [key]: next });
  };

  const togglePriority = (id: string) => {
    const value = Number(id);
    const next = prunedFilters.priorities.includes(value)
      ? prunedFilters.priorities.filter((x) => x !== value)
      : [...prunedFilters.priorities, value];
    setFilters({ ...prunedFilters, priorities: next });
  };

  const lookups = useMemo(
    () => ({
      assigneeName: (id: string) => {
        const m = (membersQuery.data ?? []).find((x) => x.id === id);
        return m ? (m.name ?? m.email) : undefined;
      },
      stateName: (id: string) =>
        (statesQuery.data ?? []).find((s) => s.id === id)?.label,
      labelName: (id: string) =>
        (labelsQuery.data ?? []).find((l) => l.id === id)?.label,
      projectName: (id: string) =>
        projects.find((p) => p.id === id)?.name,
    }),
    [membersQuery.data, statesQuery.data, labelsQuery.data, projects],
  );

  const trimmed = title.trim();
  const canSave = trimmed.length > 0 && !pending;

  const summary = filterSummary(
    { project_ids: projectIds, filters: prunedFilters },
    lookups,
  );

  const submit = () => {
    const config: WidgetConfig = {
      ...(isChartType(widgetType) ? { group_by: groupBy } : {}),
      filters: prunedFilters,
      project_ids: projectIds,
    };
    onSubmit({ title: trimmed, widget_type: widgetType, config });
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[88vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>
            {mode === "create" ? "Add widget" : "Edit widget"}
          </DialogTitle>
          <DialogDescription>
            {mode === "create"
              ? "Widgets recompute live from issue data."
              : "Change the widget's type, scope, or filters."}
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-5 py-2">
          {/* Widget type picker: all six, with preview icons. */}
          <fieldset>
            <legend className="mb-2 text-sm font-medium">Widget type</legend>
            <div
              role="radiogroup"
              aria-label="Widget type"
              className="grid grid-cols-3 gap-2 sm:grid-cols-6"
            >
              {WIDGET_TYPES.map((t) => (
                <button
                  key={t}
                  type="button"
                  role="radio"
                  aria-checked={widgetType === t}
                  onClick={() => setWidgetType(t)}
                  className={cn(
                    "flex flex-col items-center gap-1 rounded-lg border p-2 transition-colors hover:bg-accent",
                    widgetType === t
                      ? "border-primary ring-1 ring-primary"
                      : "border-border",
                  )}
                >
                  <TypeGlyph type={t} />
                  <span className="text-[11px] font-medium">
                    {WIDGET_TYPE_LABELS[t]}
                  </span>
                </button>
              ))}
            </div>
          </fieldset>

          <div className="flex flex-col gap-1.5">
            <UiLabel htmlFor="widget-title">Title</UiLabel>
            <Input
              id="widget-title"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="e.g. Issues by state"
              autoFocus
            />
          </div>

          {/* Project scope: empty = all visible (live) projects. */}
          <div className="flex flex-col gap-1.5">
            <UiLabel>Projects</UiLabel>
            <FilterPicker
              title="Projects"
              options={projects.map((p) => ({ id: p.id, label: p.name }))}
              selected={projectIds}
              onToggle={(id) =>
                setProjectIds((prev) =>
                  prev.includes(id)
                    ? prev.filter((x) => x !== id)
                    : [...prev, id],
                )
              }
              onClear={() => setProjectIds([])}
              loading={projectsQuery.isPending}
              emptyText="No projects in this workspace."
              triggerLabel={
                projectIds.length === 0
                  ? "All projects"
                  : `${projectIds.length} project${projectIds.length > 1 ? "s" : ""} selected`
              }
            />
          </div>

          {isChartType(widgetType) ? (
            <div className="flex flex-col gap-1.5">
              <UiLabel htmlFor="widget-group-by">Group by</UiLabel>
              <NativeSelect
                id="widget-group-by"
                value={groupBy}
                onChange={(e) =>
                  setGroupBy(e.target.value as WidgetGroupBy)
                }
              >
                {GROUP_BY_OPTIONS.map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label}
                  </option>
                ))}
              </NativeSelect>
            </div>
          ) : (
            <div className="rounded-md border bg-muted/50 p-3">
              <p className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
                Scope summary
              </p>
              <p className="mt-1 text-sm">{summary}</p>
            </div>
          )}

          {/* Filters. */}
          <fieldset>
            <legend className="mb-2 text-sm font-medium">Filters</legend>
            <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
              <FilterPicker
                title="Assignees"
                options={(membersQuery.data ?? []).map((m) => ({
                  id: m.id,
                  label: m.name ?? m.email,
                  hint: m.name ? m.email : undefined,
                }))}
                selected={prunedFilters.assignees}
                onToggle={(id) => toggle("assignees", id)}
                onClear={() =>
                  setFilters({ ...prunedFilters, assignees: [] })
                }
                loading={membersQuery.isPending}
                emptyText="No members in this workspace."
                triggerLabel="Assignees"
              />
              <FilterPicker
                title="Priorities"
                options={PRIORITY_LABELS.map((label, value) => ({
                  id: String(value),
                  label,
                }))}
                selected={prunedFilters.priorities.map(String)}
                onToggle={togglePriority}
                onClear={() =>
                  setFilters({ ...prunedFilters, priorities: [] })
                }
                emptyText="No priorities."
                triggerLabel="Priorities"
              />
              <FilterPicker
                title="States"
                options={statesQuery.data ?? []}
                selected={prunedFilters.states}
                onToggle={(id) => toggle("states", id)}
                onClear={() =>
                  setFilters({ ...prunedFilters, states: [] })
                }
                loading={statesQuery.isPending}
                emptyText={
                  scopeProjects.length === 0
                    ? "Select at least one project."
                    : "No states in scope."
                }
                triggerLabel="States"
              />
              <FilterPicker
                title="Labels"
                options={labelsQuery.data ?? []}
                selected={prunedFilters.labels}
                onToggle={(id) => toggle("labels", id)}
                onClear={() =>
                  setFilters({ ...prunedFilters, labels: [] })
                }
                loading={labelsQuery.isPending}
                emptyText={
                  scopeProjects.length === 0
                    ? "Select at least one project."
                    : "No labels in scope."
                }
                triggerLabel="Labels"
              />
            </div>
          </fieldset>

          {serverError && (
            <Alert variant="destructive">
              <AlertDescription>{serverError}</AlertDescription>
            </Alert>
          )}
        </div>

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => onOpenChange(false)}
          >
            Cancel
          </Button>
          <Button type="button" disabled={!canSave} onClick={submit}>
            {pending
              ? mode === "create"
                ? "Adding…"
                : "Saving…"
              : mode === "create"
                ? "Add widget"
                : "Save changes"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
