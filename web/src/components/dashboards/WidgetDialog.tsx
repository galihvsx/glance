// C83T1: create/edit widget dialog — all ten widget types with live
// preview icons, title, project multi-select (default: all visible
// projects), group-by select (hidden for `number` and `intake_breakdown`),
// and assignee/priority/state/label filter pickers reusing FilterPanel's
// CheckRow language via FilterPicker. Phase-2 per-type config panels:
// metric + max-rows + visualization toggle (smart_counter), metric +
// max-rows + gauge color + show-percentage (smart_gauge), max-rows
// (table_2d), view toggle (intake_breakdown). intake_breakdown hides the
// projects/filters sections — the backend ignores widget-level filters
// and project scope there. C100T1: the pql_table panel — a
// single-project picker (required, exactly one — the PQL translator is
// single-project-scoped), a PQL query editor reusing lib/pql's
// cheat-sheet (PqlEditor itself is a probe-and-commit control whose
// draft lives outside the form, so the dialog owns the textarea and
// reuses the cheat-sheet data + error wording instead; parse
// validation is the server's at save time, surfaced via serverError),
// and the row-list columns/sort/max-rows controls. C101T1: the
// picker became a multi-project picker (the backend
// translates the same query once per picked project and unions the
// rows). C102T1: an empty selection means all live, accessible
// workspace projects (the backend resolves empty project_ids to
// every non-archived, actor-accessible project); save requires
// only a non-blank query. Config keys mirror
// the backend's per-type allow-list (parseWidgetConfig): a key
// meaningless for the type is a 400, so each type sends exactly its
// own key set — pql_table sends query/columns/sort_by/sort_dir/
// max_rows/project_ids and NEVER filters.
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
  METRIC_OPTIONS,
  ROW_COLUMNS,
  ROW_COLUMN_LABELS,
  VISUALIZATION_OPTIONS,
  WIDGET_TYPE_LABELS,
  WIDGET_TYPES,
  filterSummary,
  isGroupedType,
  isRowListType,
  normalizeWidgetConfig,
  type DashboardWidget,
  type WidgetConfig,
  type WidgetFilters,
  type WidgetGroupBy,
  type WidgetMetric,
  type WidgetRowColumn,
  type WidgetSortDir,
  type WidgetType,
  type WidgetVisualization,
} from "../../lib/dashboards";
import { PRIORITY_LABELS } from "../../lib/types";
import { PQL_CHEAT_SHEET } from "../../lib/pql";
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
import { Textarea } from "../ui/textarea";
import { Label as UiLabel } from "../ui/label";
import { NativeSelect } from "../ui/native-select";
import { Switch } from "../ui/switch";
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
    case "smart_counter":
      return (
        <svg viewBox="0 0 48 24" className={cls} aria-hidden="true">
          {[0, 1, 2, 3].map((i) => (
            <g key={i}>
              <rect
                x={2 + (i % 2) * 24}
                y={1 + Math.floor(i / 2) * 12}
                width="22"
                height="10"
                rx="2"
                className="fill-primary/15 stroke-primary/50"
                strokeWidth="1"
              />
              <rect
                x={6 + (i % 2) * 24}
                y={5 + Math.floor(i / 2) * 12}
                width={14 - i * 2}
                height="3"
                rx="1"
                className="fill-primary/70"
              />
            </g>
          ))}
        </svg>
      );
    case "smart_gauge":
      return (
        <svg viewBox="0 0 48 24" className={cls} aria-hidden="true">
          <path
            d="M 4 21 A 20 20 0 0 1 44 21"
            fill="none"
            strokeWidth="4.5"
            strokeLinecap="round"
            className="stroke-primary/20"
          />
          <path
            d="M 4 21 A 20 20 0 0 1 32.3 9.5"
            fill="none"
            strokeWidth="4.5"
            strokeLinecap="round"
            className={fg}
          />
        </svg>
      );
    case "table_2d":
      return (
        <svg viewBox="0 0 48 24" className={cls} aria-hidden="true">
          {[0, 1, 2].map((i) => (
            <g key={i}>
              <rect
                x="6"
                y={3 + i * 7}
                width="18"
                height="4"
                rx="1"
                className="fill-primary/45"
              />
              <rect
                x="28"
                y={3 + i * 7}
                width={10 - i * 2}
                height="4"
                rx="1"
                className="fill-primary/70"
              />
            </g>
          ))}
        </svg>
      );
    case "intake_breakdown":
      return (
        <svg viewBox="0 0 48 24" className={cls} aria-hidden="true">
          {[0, 1, 2, 3, 4, 5].map((i) => (
            <rect
              key={i}
              x={2 + (i % 3) * 16}
              y={1 + Math.floor(i / 3) * 12}
              width="14"
              height="10"
              rx="2"
              className="fill-primary/25"
            />
          ))}
        </svg>
      );
    case "work_items_table":
      return (
        <svg viewBox="0 0 48 24" className={cls} aria-hidden="true">
          <rect x="2" y="2" width="44" height="20" rx="2" className="fill-primary/15 stroke-primary/50" strokeWidth="1" />
          <line x1="2" y1="9" x2="46" y2="9" className="stroke-primary/50" strokeWidth="1" />
          <line x1="2" y1="16" x2="46" y2="16" className="stroke-primary/50" strokeWidth="1" />
          <rect x="5" y="4" width="12" height="3" rx="1" className="fill-primary/70" />
          <rect x="21" y="4" width="16" height="3" rx="1" className="fill-primary/45" />
          <rect x="5" y="11" width="9" height="3" rx="1" className="fill-primary/70" />
          <rect x="18" y="11" width="20" height="3" rx="1" className="fill-primary/45" />
          <rect x="5" y="18" width="14" height="3" rx="1" className="fill-primary/45" />
        </svg>
      );
    case "assigned_to_me":
      return (
        <svg viewBox="0 0 48 24" className={cls} aria-hidden="true">
          <circle cx="24" cy="9" r="5" className="fill-primary/70" />
          <path d="M14 22 a10 6 0 0 1 20 0 Z" className="fill-primary/45" />
          <circle cx="24" cy="9" r="5" fill="none" className="stroke-primary" strokeWidth="1" />
        </svg>
      );
    case "pql_table":
      return (
        <svg viewBox="0 0 48 24" className={cls} aria-hidden="true">
          <rect x="2" y="2" width="30" height="20" rx="2" className="fill-primary/15 stroke-primary/50" strokeWidth="1" />
          <line x1="2" y1="9" x2="32" y2="9" className="stroke-primary/50" strokeWidth="1" />
          <rect x="5" y="4" width="9" height="3" rx="1" className="fill-primary/70" />
          <rect x="18" y="4" width="10" height="3" rx="1" className="fill-primary/45" />
          <rect x="5" y="11" width="14" height="3" rx="1" className="fill-primary/45" />
          <rect x="5" y="18" width="9" height="3" rx="1" className="fill-primary/45" />
          <circle cx="38" cy="15" r="4.5" fill="none" className="stroke-primary" strokeWidth="1.5" />
          <line x1="41.5" y1="18.5" x2="45" y2="22" className="stroke-primary" strokeWidth="1.5" strokeLinecap="round" />
        </svg>
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
  const [metric, setMetric] = useState<WidgetMetric>("count");
  const [maxRows, setMaxRows] = useState(10);
  const [visualization, setVisualization] =
    useState<WidgetVisualization>("grid");
  const [gaugeColor, setGaugeColor] = useState("#3b82f6");
  const [showPercentage, setShowPercentage] = useState(true);
  const [intakeView, setIntakeView] = useState<WidgetVisualization>("grid");
  const [projectIds, setProjectIds] = useState<string[]>([]);
  const [filters, setFilters] = useState<WidgetFilters>(EMPTY_WIDGET_FILTERS);
  const [columns, setColumns] = useState<WidgetRowColumn[]>([...ROW_COLUMNS]);
  const [sortBy, setSortBy] = useState<WidgetRowColumn>("state");
  const [sortDir, setSortDir] = useState<WidgetSortDir>("desc");
  // C100T1: pql_table only — the query string (dialog-owned state;
  // see the header note on why PqlEditor itself is not embedded).
  const [query, setQuery] = useState("");
  const [showCheatSheet, setShowCheatSheet] = useState(false);

  useEffect(() => {
    if (open) {
      const cfg = normalizeWidgetConfig(initial?.config ?? {});
      setTitle(initial?.title ?? "");
      setWidgetType(initial?.widget_type ?? "bar");
      setGroupBy(cfg.group_by ?? "state");
      setMetric(cfg.metric);
      setMaxRows(cfg.max_rows);
      setVisualization(cfg.visualization);
      setGaugeColor(cfg.gauge_color);
      setShowPercentage(cfg.show_percentage);
      setIntakeView(cfg.view);
      setProjectIds(initial?.config?.project_ids ?? []);
      setFilters(cfg.filters);
      // Row-list defaults: a stored config wins; a fresh
      // assigned_to_me widget defaults to all columns except
      // assignee (it is constant — the viewer), per the plan.
      if (initial?.config?.columns && initial.config.columns.length > 0) {
        setColumns(cfg.columns);
      } else if (initial?.widget_type === "assigned_to_me") {
        setColumns(ROW_COLUMNS.filter((c) => c !== "assignee"));
      } else {
        setColumns([...ROW_COLUMNS]);
      }
      setSortBy(cfg.sort_by);
      setSortDir(cfg.sort_dir);
      setQuery(initial?.config?.query ?? "");
      setShowCheatSheet(false);
    }
  }, [open, initial?.title, initial?.widget_type, initial?.config]);

  /** Switching into a row-list type resets its column default (the
   *  dialog's own state, not the stored widget — nothing is saved
   *  until submit). Switching within row-list types keeps the chips. */
  const selectType = (t: WidgetType) => {
    setWidgetType(t);
    if (isRowListType(t) && !isRowListType(widgetType)) {
      setColumns(
        t === "assigned_to_me"
          ? ROW_COLUMNS.filter((c) => c !== "assignee")
          : [...ROW_COLUMNS],
      );
      setSortBy("state");
      setSortDir("desc");
    }
  };

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
  // pql_table additionally requires a non-blank query; an empty
  // project selection is valid and means all live, accessible
  // workspace projects (C102T1), so it does not block save.
  const canSave =
    trimmed.length > 0 &&
    !pending &&
    (widgetType !== "pql_table" || query.trim().length > 0);

  const summary = filterSummary(
    { project_ids: projectIds, filters: prunedFilters },
    lookups,
  );

  const submit = () => {
    // Config keys mirror the backend's per-type allow-list
    // (parseWidgetConfig): keys meaningless for the type are a 400, so
    // each type sends exactly its own key set.
    const config: WidgetConfig = {};
    if (isGroupedType(widgetType)) {
      config.group_by = groupBy;
      config.filters = prunedFilters;
      config.project_ids = projectIds;
    } else if (widgetType === "number") {
      config.filters = prunedFilters;
      config.project_ids = projectIds;
    }
    if (widgetType === "pql_table") {
      // The backend 400s when sort_by is not one of the configured
      // columns, so a sort column dropped from the chips falls back
      // to the first kept column instead of surfacing a 400.
      // Exactly the C102T0 allow-list: query, columns, sort_by,
      // sort_dir, max_rows, project_ids — NEVER filters (PQL is the
      // filter); group_by/metric are accepted-but-ignored server-side
      // and are not sent. An empty project_ids means all live,
      // accessible workspace projects.
      const kept = columns.length > 0 ? columns : [...ROW_COLUMNS];
      config.query = query.trim();
      config.columns = kept;
      config.sort_by = kept.includes(sortBy) ? sortBy : kept[0];
      config.sort_dir = sortDir;
      config.max_rows = maxRows;
      config.project_ids = projectIds;
    } else if (isRowListType(widgetType)) {
      // The backend 400s when sort_by is not one of the configured
      // columns, so a sort column dropped from the chips falls back
      // to the first kept column instead of surfacing a 400.
      const kept = columns.length > 0 ? columns : [...ROW_COLUMNS];
      config.columns = kept;
      config.sort_by = kept.includes(sortBy) ? sortBy : kept[0];
      config.sort_dir = sortDir;
      config.max_rows = maxRows;
      config.filters = prunedFilters;
      config.project_ids = projectIds;
    }
    if (widgetType === "smart_counter" || widgetType === "smart_gauge") {
      config.metric = metric;
    }
    if (
      widgetType === "smart_counter" ||
      widgetType === "smart_gauge" ||
      widgetType === "table_2d"
    ) {
      config.max_rows = maxRows;
    }
    if (widgetType === "smart_counter") {
      config.visualization = visualization;
    }
    if (widgetType === "smart_gauge") {
      config.gauge_color = gaugeColor;
      config.show_percentage = showPercentage;
    }
    if (widgetType === "intake_breakdown") {
      config.view = intakeView;
    }
    onSubmit({ title: trimmed, widget_type: widgetType, config });
  };

  const isIntake = widgetType === "intake_breakdown";
  const isPql = widgetType === "pql_table";
  const isGauge = widgetType === "smart_gauge";
  const isCounter = widgetType === "smart_counter";
  const isTable2d = widgetType === "table_2d";
  const isRowList = isRowListType(widgetType);
  const isAssignedToMe = widgetType === "assigned_to_me";

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
          {/* Widget type picker: all ten, with preview icons. */}
          <fieldset>
            <legend className="mb-2 text-sm font-medium">Widget type</legend>
            <div
              role="radiogroup"
              aria-label="Widget type"
              className="grid grid-cols-2 gap-2 sm:grid-cols-5"
            >
              {WIDGET_TYPES.map((t) => (
                <button
                  key={t}
                  type="button"
                  role="radio"
                  aria-checked={widgetType === t}
                  onClick={() => selectType(t)}
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

          {/* Project scope: empty = all visible (live) projects. Hidden
              for intake_breakdown — the backend ignores project_ids
              there (dashboard scope only). pql_table replaces the
              generic section with its own multi-project picker below:
              the query is translated once per in-scope project and the
              rows unioned; an empty selection means all live,
              accessible workspace projects (C102T1). */}
          {!isIntake && !isPql && (
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
          )}

          {isPql && (
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
          )}

          {isGroupedType(widgetType) ? (
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
          ) : isIntake ? (
            <div className="rounded-md border bg-muted/50 p-3">
              <p className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
                Intake scope
              </p>
              <p className="mt-1 text-sm">
                Intake metrics use the dashboard's project scope —
                widget-level filters and project selection don't apply.
              </p>
            </div>
          ) : isPql ? (
            <div className="rounded-md border bg-muted/50 p-3">
              <p className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
                PQL is the filter
              </p>
              <p className="mt-1 text-sm">
                Visual filters don&apos;t apply to this widget — the
                query below is the filter. The server validates the
                query when you save.
              </p>
            </div>
          ) : (
            <div className="rounded-md border bg-muted/50 p-3">
              <p className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
                Scope summary
              </p>
              <p className="mt-1 text-sm">{summary}</p>
            </div>
          )}

          {/* Phase-2 per-type config panels. */}
          {(isCounter || isGauge) && (
            <div className="flex flex-col gap-1.5">
              <UiLabel htmlFor="widget-metric">Metric</UiLabel>
              <NativeSelect
                id="widget-metric"
                value={metric}
                onChange={(e) => setMetric(e.target.value as WidgetMetric)}
              >
                {METRIC_OPTIONS.map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label}
                  </option>
                ))}
              </NativeSelect>
            </div>
          )}

          {(isCounter || isGauge || isTable2d) && (
            <div className="flex flex-col gap-1.5">
              <UiLabel htmlFor="widget-max-rows">
                Max groups per page
              </UiLabel>
              <Input
                id="widget-max-rows"
                type="number"
                min={1}
                max={100}
                value={maxRows}
                onChange={(e) => {
                  const n = Number(e.target.value);
                  setMaxRows(
                    Number.isInteger(n)
                      ? Math.min(100, Math.max(1, n))
                      : maxRows,
                  );
                }}
              />
              <p className="text-xs text-muted-foreground">
                1–100 groups per page.
              </p>
            </div>
          )}

          {/* C100T1 PQL query panel (prototype-as-spec): the query
              editor, the ORDER BY / LIMIT precedence note, and the
              cheat-sheet reused from lib/pql (the same entries
              PqlEditor renders). A blank query blocks save; parse
              errors surface from the server on save. */}
          {isPql && (
            <div className="flex flex-col gap-1.5">
              <UiLabel htmlFor="widget-pql-query">PQL query</UiLabel>
              <Textarea
                id="widget-pql-query"
                aria-label="PQL query"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder='state = "In Progress" AND priority >= high ORDER BY priority DESC, updated_at DESC LIMIT 20'
                rows={3}
                className="font-mono text-xs"
              />
              {query.trim().length === 0 ? (
                <p className="text-xs text-destructive">
                  A PQL query is required.
                </p>
              ) : (
                <p className="text-xs text-muted-foreground">
                  Query ORDER BY wins over the sort below; LIMIT caps
                  total rows across all in-scope projects.
                </p>
              )}
              <div>
                <button
                  type="button"
                  onClick={() => setShowCheatSheet((v) => !v)}
                  aria-expanded={showCheatSheet}
                  className="text-xs font-medium text-primary hover:underline"
                >
                  {showCheatSheet ? "Hide cheat-sheet" : "Cheat-sheet"}
                </button>
                {showCheatSheet && (
                  <ul className="mt-2 space-y-1">
                    {PQL_CHEAT_SHEET.map((row) => (
                      <li key={row.example} className="text-xs">
                        <code className="rounded bg-muted px-1 py-0.5">
                          {row.example}
                        </code>{" "}
                        <span className="text-muted-foreground">
                          {row.meaning}
                        </span>
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            </div>
          )}

          {/* Phase-3 row-list config panels (C84T1). */}
          {isRowList && (
            <>
              <div className="flex flex-col gap-1.5">
                <UiLabel>Columns</UiLabel>
                <div className="flex flex-wrap gap-2" role="group" aria-label="Columns">
                  {ROW_COLUMNS.map((c) => {
                    const on = columns.includes(c);
                    const isLast = on && columns.length === 1;
                    return (
                      <button
                        key={c}
                        type="button"
                        aria-pressed={on}
                        disabled={isLast}
                        title={
                          isLast
                            ? "At least one column is required"
                            : undefined
                        }
                        onClick={() =>
                          setColumns((prev) =>
                            on
                              ? prev.filter((x) => x !== c)
                              : [...ROW_COLUMNS.filter((x) => prev.includes(x) || x === c)]
                          )
                        }
                        className={cn(
                          "rounded-full border px-3 py-1 text-xs font-medium transition-colors hover:bg-accent",
                          on
                            ? "border-primary bg-primary/10 text-primary"
                            : "border-border",
                          isLast && "cursor-not-allowed opacity-50",
                        )}
                      >
                        {ROW_COLUMN_LABELS[c]}
                      </button>
                    );
                  })}
                </div>
                <p className="text-xs text-muted-foreground">
                  Default: all columns. Unknown column → server 400
                  (surfaced here).
                </p>
              </div>

              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                <div className="flex flex-col gap-1.5">
                  <UiLabel htmlFor="widget-sort-by">Sort by</UiLabel>
                  <NativeSelect
                    id="widget-sort-by"
                    value={columns.includes(sortBy) ? sortBy : columns[0]}
                    onChange={(e) =>
                      setSortBy(e.target.value as WidgetRowColumn)
                    }
                  >
                    {columns.map((c) => (
                      <option key={c} value={c}>
                        {ROW_COLUMN_LABELS[c]}
                      </option>
                    ))}
                  </NativeSelect>
                </div>
                <div className="flex flex-col gap-1.5">
                  <UiLabel>Sort direction</UiLabel>
                  <div
                    role="radiogroup"
                    aria-label="Sort direction"
                    className="flex gap-2"
                  >
                    {(["asc", "desc"] as WidgetSortDir[]).map((d) => (
                      <button
                        key={d}
                        type="button"
                        role="radio"
                        aria-checked={sortDir === d}
                        onClick={() => setSortDir(d)}
                        className={cn(
                          "rounded-lg border px-3 py-2 text-[13px] font-medium transition-colors hover:bg-accent",
                          sortDir === d
                            ? "border-primary ring-1 ring-primary"
                            : "border-border",
                        )}
                      >
                        {d === "asc" ? "Asc" : "Desc"}
                      </button>
                    ))}
                  </div>
                </div>
              </div>

              <div className="flex flex-col gap-1.5">
                <UiLabel htmlFor="widget-max-rows-list">Max rows</UiLabel>
                <Input
                  id="widget-max-rows-list"
                  type="number"
                  min={1}
                  max={100}
                  value={maxRows}
                  onChange={(e) => {
                    const n = Number(e.target.value);
                    setMaxRows(
                      Number.isInteger(n)
                        ? Math.min(100, Math.max(1, n))
                        : maxRows,
                    );
                  }}
                />
                <p className="text-xs text-muted-foreground">
                  1–100 rows per page.
                </p>
              </div>

              {isAssignedToMe && (
                <div className="rounded-md border bg-muted/50 p-3">
                  <p className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
                    Assigned to you
                  </p>
                  <p className="mt-1 text-sm">
                    Assignee is always you — filter is forced. A
                    conflicting assignee filter is overridden server-side.
                  </p>
                </div>
              )}
            </>
          )}

          {isCounter && (
            <div className="flex flex-col gap-1.5">
              <UiLabel>Visualization</UiLabel>
              <div
                role="radiogroup"
                aria-label="Smart counter visualization"
                className="grid grid-cols-2 gap-2"
              >
                {VISUALIZATION_OPTIONS.map((o) => (
                  <button
                    key={o.value}
                    type="button"
                    role="radio"
                    aria-checked={visualization === o.value}
                    onClick={() => setVisualization(o.value)}
                    className={cn(
                      "rounded-lg border p-2 text-[13px] font-medium transition-colors hover:bg-accent",
                      visualization === o.value
                        ? "border-primary ring-1 ring-primary"
                        : "border-border",
                    )}
                  >
                    {o.label}
                  </button>
                ))}
              </div>
            </div>
          )}

          {isGauge && (
            <div className="flex flex-col gap-4">
              <div className="flex flex-col gap-1.5">
                <UiLabel htmlFor="widget-gauge-color">Gauge color</UiLabel>
                <div className="flex items-center gap-2">
                  <input
                    id="widget-gauge-color"
                    type="color"
                    value={gaugeColor}
                    onChange={(e) => setGaugeColor(e.target.value)}
                    className="size-9 cursor-pointer rounded border border-border bg-transparent p-0.5"
                    aria-label="Gauge color"
                  />
                  <Input
                    value={gaugeColor}
                    onChange={(e) => setGaugeColor(e.target.value)}
                    aria-label="Gauge color hex"
                    className="w-28 font-mono"
                    spellCheck={false}
                  />
                </div>
              </div>
              <div className="flex items-center justify-between gap-2">
                <UiLabel htmlFor="widget-show-percentage">
                  Show percentage label
                </UiLabel>
                <Switch
                  id="widget-show-percentage"
                  checked={showPercentage}
                  onCheckedChange={setShowPercentage}
                />
              </div>
            </div>
          )}

          {isIntake && (
            <div className="flex flex-col gap-1.5">
              <UiLabel>View</UiLabel>
              <div
                role="radiogroup"
                aria-label="Intake breakdown view"
                className="grid grid-cols-2 gap-2"
              >
                {VISUALIZATION_OPTIONS.map((o) => (
                  <button
                    key={o.value}
                    type="button"
                    role="radio"
                    aria-checked={intakeView === o.value}
                    onClick={() => setIntakeView(o.value)}
                    className={cn(
                      "rounded-lg border p-2 text-[13px] font-medium transition-colors hover:bg-accent",
                      intakeView === o.value
                        ? "border-primary ring-1 ring-primary"
                        : "border-border",
                    )}
                  >
                    {o.label}
                  </button>
                ))}
              </div>
            </div>
          )}

          {/* Filters — hidden for intake_breakdown (the backend ignores
              widget-level filters there; showing them would be a lie)
              and for pql_table (PQL is the filter; sending a filters
              key is a server 400). */}
          {!isIntake && !isPql && (
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
          )}

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
