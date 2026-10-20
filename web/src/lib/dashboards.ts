// Dashboards API client + types (C81T1). Hand-written against the
// pinned OpenAPI schemas in docs/openapi.yaml (Dashboard,
// DashboardWidget, WidgetConfig, WidgetData, WidgetDataGroup) — the
// repo generates no TS from OpenAPI (see lib/analytics.ts), so this
// module is the typed surface for the dashboards UI. Edit rule is
// Plane's exactly (see internal/service/dashboard.go): creator, or a
// workspace admin (20) on PUBLIC dashboards only.

import { api } from "./api";

/** Dashboard visibility: public (default) or private. */
export type DashboardVisibility = "public" | "private";

/** The six Plane widget types (phase 1). */
export type WidgetType = "bar" | "line" | "area" | "donut" | "pie" | "number";

export const WIDGET_TYPES: WidgetType[] = [
  "bar",
  "line",
  "area",
  "donut",
  "pie",
  "number",
];

/** Human label per widget type (dialog picker + canvas fallbacks). */
export const WIDGET_TYPE_LABELS: Record<WidgetType, string> = {
  bar: "Bar chart",
  line: "Line chart",
  area: "Area chart",
  donut: "Donut chart",
  pie: "Pie chart",
  number: "Number",
};

/** Chart types that need a group_by; number is the scalar. */
export function isChartType(t: WidgetType): boolean {
  return t !== "number";
}

/** Grouping dimension for chart widgets. */
export type WidgetGroupBy = "state" | "priority" | "assignee" | "label";

export const GROUP_BY_OPTIONS: { value: WidgetGroupBy; label: string }[] = [
  { value: "state", label: "State" },
  { value: "priority", label: "Priority" },
  { value: "assignee", label: "Assignee" },
  { value: "label", label: "Label" },
];

export interface WidgetFilters {
  assignees: string[];
  priorities: number[];
  states: string[];
  labels: string[];
}

export const EMPTY_WIDGET_FILTERS: WidgetFilters = {
  assignees: [],
  priorities: [],
  states: [],
  labels: [],
};

export interface WidgetConfig {
  group_by?: WidgetGroupBy;
  metric?: "count";
  filters?: Partial<WidgetFilters>;
  project_ids?: string[];
}

export interface DashboardWidget {
  id: string;
  dashboard_id: string;
  title: string;
  widget_type: WidgetType;
  config: WidgetConfig;
  grid_x: number;
  grid_y: number;
  grid_w: number;
  grid_h: number;
  created_at: string;
  updated_at: string;
}

export interface Dashboard {
  id: string;
  workspace_id: string;
  name: string;
  description: string | null;
  visibility: DashboardVisibility;
  created_by: string;
  created_by_name: string;
  created_at: string;
  updated_at: string;
  /** Present on the detail read only (config only — fetch data per widget). */
  widgets?: DashboardWidget[];
}

export interface WidgetDataGroup {
  key: string;
  label: string;
  value: number;
}

export interface WidgetData {
  groups?: WidgetDataGroup[];
  value?: number;
}

/* ------------------------------------------------------------------ */
/* Query keys.                                                         */
/* ------------------------------------------------------------------ */

export const dashboardsKey = (slug: string) => ["dashboards", slug] as const;
export const dashboardKey = (slug: string, id: string) =>
  ["dashboard", slug, id] as const;
export const widgetDataKey = (widgetId: string) =>
  ["widget-data", widgetId] as const;

/* ------------------------------------------------------------------ */
/* API.                                                                */
/* ------------------------------------------------------------------ */

function dashBase(slug: string): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/dashboards`;
}

export function fetchDashboards(slug: string): Promise<Dashboard[]> {
  return api
    .get<{ dashboards: Dashboard[] }>(dashBase(slug))
    .then((d) => d.dashboards);
}

export function createDashboard(
  slug: string,
  v: { name: string; description?: string; visibility: DashboardVisibility },
): Promise<Dashboard> {
  return api.post<Dashboard>(dashBase(slug), {
    name: v.name,
    ...(v.description ? { description: v.description } : {}),
    visibility: v.visibility,
  });
}

export function fetchDashboard(slug: string, id: string): Promise<Dashboard> {
  return api.get<Dashboard>(`${dashBase(slug)}/${encodeURIComponent(id)}`);
}

export function updateDashboard(
  slug: string,
  id: string,
  patch: Partial<{
    name: string;
    description: string | null;
    visibility: DashboardVisibility;
  }>,
): Promise<Dashboard> {
  return api.patch<Dashboard>(`${dashBase(slug)}/${encodeURIComponent(id)}`, patch);
}

export function deleteDashboard(slug: string, id: string): Promise<void> {
  return api.del<void>(`${dashBase(slug)}/${encodeURIComponent(id)}`);
}

export interface CreateWidgetInput {
  title: string;
  widget_type: WidgetType;
  config: WidgetConfig;
  grid_x: number;
  grid_y: number;
  grid_w: number;
  grid_h: number;
}

export function createWidget(
  slug: string,
  dashboardId: string,
  v: CreateWidgetInput,
): Promise<DashboardWidget> {
  return api.post<DashboardWidget>(
    `${dashBase(slug)}/${encodeURIComponent(dashboardId)}/widgets`,
    v,
  );
}

export function updateWidget(
  widgetId: string,
  patch: Partial<Pick<DashboardWidget, "title" | "widget_type" | "config">>,
): Promise<DashboardWidget> {
  return api.patch<DashboardWidget>(
    `/api/v1/widgets/${encodeURIComponent(widgetId)}`,
    patch,
  );
}

export interface LayoutPatch {
  grid_x: number;
  grid_y: number;
  grid_w: number;
  grid_h: number;
}

/** Drag/resize commit: the layout endpoint accepts grid_* only. */
export function patchWidgetLayout(
  widgetId: string,
  layout: LayoutPatch,
): Promise<DashboardWidget> {
  return api.patch<DashboardWidget>(
    `/api/v1/widgets/${encodeURIComponent(widgetId)}/layout`,
    layout,
  );
}

/** The exact PATCH body shape committed on drop (grid keys only). */
export function layoutPatchBody(layout: {
  grid_x: number;
  grid_y: number;
  grid_w: number;
  grid_h: number;
}): LayoutPatch {
  return {
    grid_x: layout.grid_x,
    grid_y: layout.grid_y,
    grid_w: layout.grid_w,
    grid_h: layout.grid_h,
  };
}

export function deleteWidget(widgetId: string): Promise<void> {
  return api.del<void>(`/api/v1/widgets/${encodeURIComponent(widgetId)}`);
}

export function fetchWidgetData(widgetId: string): Promise<WidgetData> {
  return api.get<WidgetData>(
    `/api/v1/widgets/${encodeURIComponent(widgetId)}/data`,
  );
}

/* ------------------------------------------------------------------ */
/* Pure helpers (unit-tested).                                         */
/* ------------------------------------------------------------------ */

/**
 * Plane's edit rule for the frontend affordance gate (the server
 * enforces): the creator, or a workspace admin (20) on PUBLIC
 * dashboards; on private dashboards only the creator.
 */
export function canEditDashboard(
  dashboard: Pick<Dashboard, "created_by" | "visibility">,
  userId: string | null | undefined,
  workspaceRole: number,
): boolean {
  if (!userId) return false;
  if (dashboard.created_by === userId) return true;
  return dashboard.visibility === "public" && workspaceRole >= 20;
}

/** Fill the config defaults the backend assumes (empty = unfiltered). */
export function normalizeWidgetConfig(config: WidgetConfig): {
  group_by?: WidgetGroupBy;
  metric: "count";
  filters: WidgetFilters;
  project_ids: string[];
} {
  const f = config.filters ?? {};
  return {
    ...(config.group_by ? { group_by: config.group_by } : {}),
    metric: "count",
    filters: {
      assignees: f.assignees ?? [],
      priorities: f.priorities ?? [],
      states: f.states ?? [],
      labels: f.labels ?? [],
    },
    project_ids: config.project_ids ?? [],
  };
}

/** WidgetData.groups → recharts series rows. */
export function chartSeries(
  groups: WidgetDataGroup[] | undefined,
): { name: string; value: number }[] {
  return (groups ?? []).map((g) => ({ name: g.label, value: g.value }));
}

/** Scalar for number widgets; null when the backend sent nothing. */
export function numberValue(data: WidgetData | undefined): number | null {
  return typeof data?.value === "number" ? data.value : null;
}

/** Categorical palette for chart series (index-cycled). */
export const CHART_PALETTE = [
  "#60a5fa",
  "#34d399",
  "#fbbf24",
  "#f87171",
  "#a78bfa",
  "#22d3ee",
  "#fb923c",
  "#f472b6",
  "#94a3b8",
  "#4ade80",
] as const;

export function seriesColor(index: number): string {
  return CHART_PALETTE[index % CHART_PALETTE.length];
}

/** Short filter summary for the number-widget dialog section. */
export function filterSummary(
  config: WidgetConfig,
  lookups: {
    assigneeName: (id: string) => string | undefined;
    stateName: (id: string) => string | undefined;
    labelName: (id: string) => string | undefined;
    projectName: (id: string) => string | undefined;
  },
): string {
  const f = normalizeWidgetConfig(config).filters;
  const parts: string[] = [];
  if (config.project_ids && config.project_ids.length > 0) {
    parts.push(
      `Projects: ${config.project_ids.map((id) => lookups.projectName(id) ?? id).join(", ")}`,
    );
  }
  if (f.assignees.length > 0) {
    parts.push(
      `Assignees: ${f.assignees.map((id) => lookups.assigneeName(id) ?? id).join(", ")}`,
    );
  }
  if (f.priorities.length > 0) {
    parts.push(`Priorities: ${f.priorities.join(", ")}`);
  }
  if (f.states.length > 0) {
    parts.push(
      `States: ${f.states.map((id) => lookups.stateName(id) ?? id).join(", ")}`,
    );
  }
  if (f.labels.length > 0) {
    parts.push(
      `Labels: ${f.labels.map((id) => lookups.labelName(id) ?? id).join(", ")}`,
    );
  }
  return parts.length > 0 ? parts.join(" · ") : "All issues, all projects";
}
