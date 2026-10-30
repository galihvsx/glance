// Dashboards API client + types (C81T1). Hand-written against the
// pinned OpenAPI schemas in docs/openapi.yaml (Dashboard,
// DashboardWidget, WidgetConfig, WidgetData, WidgetDataGroup) — the
// repo generates no TS from OpenAPI (see lib/analytics.ts), so this
// module is the typed surface for the dashboards UI. Edit rule is
// Plane's exactly (see internal/service/dashboard.go): creator, or a
// workspace admin (20) on PUBLIC dashboards only.

import { ApiError, api } from "./api";

/** Dashboard visibility: public (default) or private. */
export type DashboardVisibility = "public" | "private";

/** The six Plane widget types (phase 1) plus the four phase-2 types (C83T0)
 *  plus the two phase-3 row-list types (C84T0). */
export type WidgetType =
  | "bar"
  | "line"
  | "area"
  | "donut"
  | "pie"
  | "number"
  | "smart_counter"
  | "smart_gauge"
  | "table_2d"
  | "intake_breakdown"
  | "work_items_table"
  | "assigned_to_me";

export const WIDGET_TYPES: WidgetType[] = [
  "bar",
  "line",
  "area",
  "donut",
  "pie",
  "number",
  "smart_counter",
  "smart_gauge",
  "table_2d",
  "intake_breakdown",
  "work_items_table",
  "assigned_to_me",
];

/** Human label per widget type (dialog picker + canvas fallbacks). */
export const WIDGET_TYPE_LABELS: Record<WidgetType, string> = {
  bar: "Bar chart",
  line: "Line chart",
  area: "Area chart",
  donut: "Donut chart",
  pie: "Pie chart",
  number: "Number",
  smart_counter: "Smart counter",
  smart_gauge: "Smart gauge",
  table_2d: "Table (2D)",
  intake_breakdown: "Intake breakdown",
  work_items_table: "Work items table",
  assigned_to_me: "Assigned to you",
};

/** Chart types that need a group_by; number is the scalar. */
export function isChartType(t: WidgetType): boolean {
  return (
    t === "bar" ||
    t === "line" ||
    t === "area" ||
    t === "donut" ||
    t === "pie"
  );
}

/** Types whose config requires group_by (charts + the grouped
 *  phase-2 types); number and intake_breakdown take none. */
export function isGroupedType(t: WidgetType): boolean {
  return (
    isChartType(t) ||
    t === "smart_counter" ||
    t === "smart_gauge" ||
    t === "table_2d"
  );
}

/** Phase-2 types with a paginated data envelope ({groups, page,
 *  max_rows, total_groups}). */
export function isPaginatedType(t: WidgetType): boolean {
  return t === "smart_counter" || t === "smart_gauge" || t === "table_2d";
}

/** Phase-3 row-list types (C84T0): a paginated row envelope
 *  ({rows, page, max_rows, total}) instead of groups. */
export function isRowListType(t: WidgetType): boolean {
  return t === "work_items_table" || t === "assigned_to_me";
}

/** Types whose segments are clickable into the drill-down modal
 *  (C84T0): every group-based widget. number, intake_breakdown, and
 *  the row-list types themselves have no groups to drill into. */
export function isDrillableType(t: WidgetType): boolean {
  return (
    isChartType(t) ||
    t === "smart_counter" ||
    t === "smart_gauge" ||
    t === "table_2d"
  );
}

/** Row-list column vocabulary (C84T0 backend contract), in display
 *  order. The backend default is all seven. */
export type WidgetRowColumn =
  | "id"
  | "title"
  | "state"
  | "priority"
  | "assignee"
  | "labels"
  | "project";

export const ROW_COLUMNS: WidgetRowColumn[] = [
  "id",
  "title",
  "state",
  "priority",
  "assignee",
  "labels",
  "project",
];

export const ROW_COLUMN_LABELS: Record<WidgetRowColumn, string> = {
  id: "ID",
  title: "Title",
  state: "State",
  priority: "Priority",
  assignee: "Assignee",
  labels: "Labels",
  project: "Project",
};

export type WidgetSortDir = "asc" | "desc";

/** Grouping dimension for chart and grouped phase-2 widgets. The full
 *  set the backend accepts (phase 2 adds state_group, project,
 *  created_by). */
export type WidgetGroupBy =
  | "state"
  | "priority"
  | "assignee"
  | "label"
  | "state_group"
  | "project"
  | "created_by";

export const GROUP_BY_OPTIONS: { value: WidgetGroupBy; label: string }[] = [
  { value: "state", label: "State" },
  { value: "priority", label: "Priority" },
  { value: "assignee", label: "Assignee" },
  { value: "label", label: "Label" },
  { value: "state_group", label: "State group" },
  { value: "project", label: "Project" },
  { value: "created_by", label: "Created by" },
];

/** Metric for smart_counter/smart_gauge: issue count, or summed
 *  estimate points. */
export type WidgetMetric = "count" | "estimate_points";

export const METRIC_OPTIONS: { value: WidgetMetric; label: string }[] = [
  { value: "count", label: "Issue count" },
  { value: "estimate_points", label: "Estimate points" },
];

/** smart_counter `visualization` / intake_breakdown `view`. */
export type WidgetVisualization = "grid" | "table";

export const VISUALIZATION_OPTIONS: {
  value: WidgetVisualization;
  label: string;
}[] = [
  { value: "grid", label: "Grid" },
  { value: "table", label: "Table" },
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
  metric?: WidgetMetric;
  filters?: Partial<WidgetFilters>;
  project_ids?: string[];
  /** smart_counter|smart_gauge|table_2d only: groups per page (1–100). */
  max_rows?: number;
  /** smart_counter only: tile grid or table render. */
  visualization?: WidgetVisualization;
  /** smart_gauge only: #rrggbb arc color. */
  gauge_color?: string;
  /** smart_gauge only: render the % label. */
  show_percentage?: boolean;
  /** intake_breakdown only: tile grid or table render. */
  view?: WidgetVisualization;
  /** work_items_table|assigned_to_me only: rendered columns, in order
   *  (default: all seven). The payload always carries the full row. */
  columns?: WidgetRowColumn[];
  /** work_items_table|assigned_to_me only: sort column (default:
   *  "state"; must sit inside `columns` or the backend 400s). */
  sort_by?: WidgetRowColumn;
  /** work_items_table|assigned_to_me only: sort direction (default:
   *  "desc"). */
  sort_dir?: WidgetSortDir;
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
  /** True when the caller holds a share row on someone else's dashboard. */
  shared_with_me?: boolean;
  /** The caller's effective access: drives frontend affordance gating. */
  my_access?: DashboardAccess;
  /** True on dashboards the caller owns that have at least one share. */
  shared?: boolean;
}

/** The caller's effective access on a dashboard (C83T0 contract). */
export type DashboardAccess = "owner" | "view" | "edit";

export interface WidgetDataGroup {
  key: string;
  label: string;
  value: number;
  /** smart_gauge only: the completed share of the group. */
  completed?: number;
  /** smart_gauge only: 100*completed/value (0 when value is 0). */
  percentage?: number;
}

/** The six headline numbers of an intake_breakdown widget. email_volume
 *  is always 0 — glance has no email ingestion. */
export interface IntakeBreakdown {
  in_app_volume: number;
  email_volume: number;
  form_volume: number;
  total_accepted: number;
  total_rejected: number;
  avg_decide_seconds: number;
  /** C85T0 funnel fields (new; older fixtures may omit them). */
  total_duplicate?: number;
  pending?: number;
  snoozed?: number;
}

/** The eight row-set metrics of the intake_breakdown drill-down
 *  (C85T0): every tile except the avg-decide aggregate. */
export type IntakeMetric =
  | "in_app"
  | "form"
  | "email"
  | "accepted"
  | "rejected"
  | "duplicate"
  | "pending"
  | "snoozed";

export const INTAKE_METRIC_LABELS: Record<IntakeMetric, string> = {
  in_app: "In-app volume",
  form: "Form volume",
  email: "Email volume",
  accepted: "Total accepted",
  rejected: "Total rejected",
  duplicate: "Total duplicate",
  pending: "Pending",
  snoozed: "Snoozed",
};

/** One intake row behind an intake_breakdown tile (C85T0): the
 *  drill-down payload shape. `id` is the intake row id; `issue_id`
 *  is the issue UUID the row-click navigation needs. `status_name`
 *  is the effective status (pending includes resurfaced snoozed,
 *  snoozed is still-snoozed only — the inbox EffectiveStatus rule). */
export interface IntakeDrilldownItem {
  id: string;
  issue_id: string;
  title: string;
  project: { id: string; name: string };
  status: number;
  status_name: string;
  source: string;
  snoozed_till?: string | null;
  duplicate_to_id?: string | null;
  created_at: string;
}

/** The intake drill-down envelope (C85T0): per_page defaults to 10,
 *  capped at 100 — the drill-down per_page convention. C87T0 echoes
 *  `granularity` + `bucket_start` only when the bucket filter is
 *  present; a bucket without a metric carries metric "" (all rows in
 *  the bucket), not a missing-metric error. */
export interface IntakeDrilldown {
  items: IntakeDrilldownItem[];
  page: number;
  per_page: number;
  total: number;
  metric: string;
  granularity?: string;
  bucket_start?: string;
}

/** Map an intake drill-down row onto the C84 row-click shape: the
 *  issue-detail route needs the issue UUID (issue_id, NOT the intake
 *  row id) plus the project id for identifier resolution. */
export function intakeDrilldownRowToWidgetRow(
  item: IntakeDrilldownItem,
): WidgetRowItem {
  return {
    id: item.issue_id,
    title: item.title,
    state: item.status_name,
    priority: 0,
    assignee: null,
    labels: [],
    project: item.project,
  };
}

/** One canonical work-item row (C84T0): the row-list payload, the
 *  drill-down payload, and the row CSV export all share this shape.
 *  `state` is the state NAME (human label); `state_group` is the
 *  machine slug. `id` is the issue UUID — the issue-detail route
 *  needs the project identifier, resolved by the card from the
 *  workspace project list (the row carries only {id, name}). */
export interface WidgetRowItem {
  id: string;
  title: string;
  state: string;
  state_group?: string;
  priority: number;
  assignee: { id: string; name: string } | null;
  labels: { id: string; name: string; color: string }[];
  project: { id: string; name: string };
}

/** The drill-down envelope (C84T0): the items behind one chart
 *  segment, most-recently-updated first. `per_page` is the widget's
 *  max_rows; `group_label` echoes the human group key. */
export interface WidgetDrilldown {
  items: WidgetRowItem[];
  page: number;
  per_page: number;
  total: number;
  group_label: string;
  group_by: string;
}

export interface WidgetData {
  groups?: WidgetDataGroup[];
  value?: number;
  /** smart_counter|smart_gauge|table_2d only: the returned page. */
  page?: number;
  /** smart_counter|smart_gauge|table_2d only: groups per page. */
  max_rows?: number;
  /** smart_counter|smart_gauge|table_2d only: total groups before pagination. */
  total_groups?: number;
  /** Present only for intake_breakdown widgets. */
  intake?: IntakeBreakdown;
  /** Present only for work_items_table|assigned_to_me: the page's rows. */
  rows?: WidgetRowItem[];
  /** Present only for work_items_table|assigned_to_me: total rows. */
  total?: number;
}

/* ------------------------------------------------------------------ */
/* Query keys.                                                         */
/* ------------------------------------------------------------------ */

export const dashboardsKey = (slug: string) => ["dashboards", slug] as const;
export const dashboardKey = (slug: string, id: string) =>
  ["dashboard", slug, id] as const;
/** Paginated widget types cache per page (the paginated envelope is
 *  page-specific). */
export const widgetDataKey = (widgetId: string, page = 1) =>
  ["widget-data", widgetId, page] as const;

/** Drill-down modal cache per widget + group key + page. */
export const widgetDrilldownKey = (
  widgetId: string,
  group: string,
  page = 1,
) => ["widget-drilldown", widgetId, group, page] as const;

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

export function fetchWidgetData(
  widgetId: string,
  page = 1,
): Promise<WidgetData> {
  return api.get<WidgetData>(
    `/api/v1/widgets/${encodeURIComponent(widgetId)}/data?page=${page}`,
  );
}

/** The items behind one chart segment (C84T0). `group` is the
 *  widget's own human-label group key (the data endpoint's key or
 *  label — the backend matches either). */
export function fetchWidgetDrilldown(
  widgetId: string,
  group: string,
  page = 1,
): Promise<WidgetDrilldown> {
  return api.get<WidgetDrilldown>(
    `/api/v1/widgets/${encodeURIComponent(widgetId)}/drilldown?group=${encodeURIComponent(group)}&page=${page}`,
  );
}

/** Intake drill-down modal cache per widget + metric + page. */
export const widgetIntakeDrilldownKey = (
  widgetId: string,
  metric: string,
  page = 1,
) => ["widget-intake-drilldown", widgetId, metric, page] as const;

/* ------------------------------------------------------------------ */
/* Intake trend (C86T0/T1): the time-bucketed intake series behind    */
/* GET /api/v1/widgets/:id/trend — consecutive periods ending at the  */
/* current period, so the series always ends "now". Buckets count     */
/* rows by CREATION bucket, not decision time (a row decided this     */
/* week but created last month counts in last month's bucket), so     */
/* created volume vs decided volume stay comparable within a bucket.  */
/* Email is always 0 — glance has no email ingestion; the series is   */
/* present as all-zeros so the chart legend stays honest, never       */
/* faked. Empty buckets render as zeros (no gaps). Scope = every      */
/* non-archived project of the workspace; widget-level filters and    */
/* project_ids are ignored, like the headline numbers.                */
/* ------------------------------------------------------------------ */

/** Intake trend granularities (C86T0): day = calendar days, week = ISO
 *  weeks starting Monday, month = calendar months (all UTC). */
export type IntakeTrendGranularity = "day" | "week" | "month";

/** The granularity toggle options, in toggle order (default Week). */
export const INTAKE_TREND_GRANULARITIES: {
  value: IntakeTrendGranularity;
  label: string;
  period: string;
}[] = [
  { value: "day", label: "Day", period: "days" },
  { value: "week", label: "Week", period: "weeks" },
  { value: "month", label: "Month", period: "months" },
];

/** The trend bucket count is fixed at 12 — no bucket-count control
 *  (C86T1 scope: the toggle is granularity only). */
export const INTAKE_TREND_BUCKETS = 12;

/** One period of the intake trend series. Field names reuse the
 *  IntakeBreakdown vocabulary — no synonyms. */
export interface IntakeTrendBucket {
  bucket_start: string;
  in_app: number;
  form: number;
  email: number;
  accepted: number;
  rejected: number;
  duplicate: number;
}

/** The intake trend envelope: the granularity echoed back, the
 *  zero-filled buckets, and the bucket count. */
export interface IntakeTrend {
  granularity: string;
  buckets: IntakeTrendBucket[];
  total_buckets: number;
}

/** Intake trend cache per widget + granularity (bucket count fixed). */
export const widgetIntakeTrendKey = (
  widgetId: string,
  granularity: IntakeTrendGranularity,
) => ["widget-intake-trend", widgetId, granularity] as const;

/** Fetch the intake trend series for an intake_breakdown widget:
 *  `?granularity=<g>&buckets=12` (unknown granularity or an
 *  out-of-range bucket count is an honest server 400, surfaced by
 *  the caller; a non-intake widget is a 400 too). */
export function fetchIntakeTrend(
  widgetId: string,
  granularity: IntakeTrendGranularity,
  buckets: number = INTAKE_TREND_BUCKETS,
): Promise<IntakeTrend> {
  return api.get<IntakeTrend>(
    `/api/v1/widgets/${encodeURIComponent(widgetId)}/trend?granularity=${encodeURIComponent(granularity)}&buckets=${buckets}`,
  );
}

/** Created volume in a bucket: in-app + form (+ email, always 0). */
export function trendVolume(bucket: IntakeTrendBucket): number {
  return bucket.in_app + bucket.form + bucket.email;
}

/** Decided volume in a bucket: accepted + rejected + duplicate
 *  (by creation bucket, not decision time — see the header above). */
export function trendDecided(bucket: IntakeTrendBucket): number {
  return bucket.accepted + bucket.rejected + bucket.duplicate;
}

/** The intake rows behind one intake_breakdown tile (C85T0).
 *  `metric` is one of the eight row-set metrics; avg_decide and
 *  unknown metrics are honest server 400s, surfaced by the caller. */
export function fetchIntakeDrilldown(
  widgetId: string,
  metric: string,
  page = 1,
): Promise<IntakeDrilldown> {
  return api.get<IntakeDrilldown>(
    `/api/v1/widgets/${encodeURIComponent(widgetId)}/drilldown?metric=${encodeURIComponent(metric)}&page=${page}`,
  );
}

/** Intake bucket drill-down cache per widget + granularity +
 *  bucket_start + page. The granularity is part of the key: the
 *  same bucket_start under a different granularity is a different
 *  window, never a cache hit. */
export const widgetIntakeDrilldownBucketKey = (
  widgetId: string,
  granularity: IntakeTrendGranularity,
  bucket_start: string,
  page = 1,
) =>
  [
    "widget-intake-drilldown-bucket",
    widgetId,
    granularity,
    bucket_start,
    page,
  ] as const;

/** The intake rows created in one trend bucket (C87T0/T1): no
 *  `metric` param — the server reads that as "all rows in the
 *  bucket" (any source/status), and owns the window math from
 *  granularity + bucket_start alone. The client NEVER recomputes
 *  the bucket window: bucket_start comes verbatim from the trend
 *  payload's clicked point. Pagination matches the metric modal
 *  (page/per_page, per_page defaults to the widget's max_rows). */
export function fetchIntakeDrilldownBucket(
  widgetId: string,
  granularity: IntakeTrendGranularity,
  bucket_start: string,
  page = 1,
): Promise<IntakeDrilldown> {
  return api.get<IntakeDrilldown>(
    `/api/v1/widgets/${encodeURIComponent(widgetId)}/drilldown?granularity=${encodeURIComponent(granularity)}&bucket_start=${encodeURIComponent(bucket_start)}&page=${page}`,
  );
}

const INTAKE_BUCKET_MONTH_NAMES = [
  "January",
  "February",
  "March",
  "April",
  "May",
  "June",
  "July",
  "August",
  "September",
  "October",
  "November",
  "December",
];

/** The bucket drill-down modal title (C87T1, prototype-as-spec):
 *  "Intake — week of <Monday date>" / "day of <date>" / "month of
 *  <Month YYYY>". Day/week reuse the server's bucket_start verbatim
 *  (an ISO date, never re-rendered through the local timezone);
 *  month renders the YYYY-MM prefix as a month name + year. A
 *  malformed month start falls back to the raw value rather than
 *  inventing a date. */
export function intakeBucketTitle(
  granularity: IntakeTrendGranularity,
  bucket_start: string,
): string {
  if (granularity === "month") {
    const m = /^(\d{4})-(\d{2})-\d{2}$/.exec(bucket_start);
    if (m) {
      const month = INTAKE_BUCKET_MONTH_NAMES[Number(m[2]) - 1];
      if (month) return `Intake — month of ${month} ${m[1]}`;
    }
    return `Intake — month of ${bucket_start}`;
  }
  if (granularity === "day") return `Intake — day of ${bucket_start}`;
  return `Intake — week of ${bucket_start}`;
}

/* ------------------------------------------------------------------ */
/* Shares (C83T1): owner-only batch management.                        */
/* ------------------------------------------------------------------ */

/** Access level on a dashboard share: view or edit. */
export type ShareAccess = "view" | "edit";

/** One member's share grant on a dashboard (GET shares contract:
 *  member_id, member_name, access, created_at; oldest first). */
export interface DashboardShare {
  member_id: string;
  member_name: string;
  access: ShareAccess;
  created_at: string;
}

/** The whole desired share list for the one-batch PUT (empty/absent
 *  clears every share). */
export interface ShareInput {
  member_id: string;
  access: ShareAccess;
}

function sharesBase(slug: string, dashboardId: string): string {
  return `/api/v1/workspaces/${encodeURIComponent(slug)}/dashboards/${encodeURIComponent(dashboardId)}/shares`;
}

export function fetchDashboardShares(
  slug: string,
  dashboardId: string,
): Promise<DashboardShare[]> {
  return api
    .get<{ shares: DashboardShare[] }>(sharesBase(slug, dashboardId))
    .then((d) => d.shares ?? []);
}

export function replaceDashboardShares(
  slug: string,
  dashboardId: string,
  shares: ShareInput[],
): Promise<DashboardShare[]> {
  return api
    .put<{ shares: DashboardShare[] }>(sharesBase(slug, dashboardId), {
      shares,
    })
    .then((d) => d.shares ?? []);
}

/* ------------------------------------------------------------------ */
/* Templates (C83T1): static create-from-template catalog.             */
/* ------------------------------------------------------------------ */

export interface DashboardTemplateWidget {
  title: string;
  widget_type: string;
  config: Record<string, unknown>;
  grid_x: number;
  grid_y: number;
  grid_w: number;
  grid_h: number;
}

export interface DashboardTemplate {
  id: string;
  name: string;
  description: string;
  widgets: DashboardTemplateWidget[];
}

export function fetchDashboardTemplates(
  slug: string,
): Promise<DashboardTemplate[]> {
  return api
    .get<{ templates: DashboardTemplate[] }>(
      `/api/v1/workspaces/${encodeURIComponent(slug)}/dashboard-templates`,
    )
    .then((d) => d.templates ?? []);
}

export function createDashboardFromTemplate(
  slug: string,
  v: { template: string; name?: string },
): Promise<Dashboard> {
  return api.post<Dashboard>(dashBase(slug), {
    template: v.template,
    ...(v.name ? { name: v.name } : {}),
  });
}

/* ------------------------------------------------------------------ */
/* CSV export (C83T1): full live dataset download.                     */
/* ------------------------------------------------------------------ */

/** Parse the filename out of a Content-Disposition attachment header. */
export function exportFilename(
  contentDisposition: string | null,
  fallback: string,
): string {
  if (contentDisposition) {
    const m = /filename="([^"]+)"/.exec(contentDisposition);
    if (m) return m[1];
  }
  return fallback;
}

export interface CsvDownload {
  blob: Blob;
  filename: string;
  /** True when the server capped the response at 10,000 rows. */
  truncated: boolean;
}

export async function fetchWidgetCsv(
  widgetId: string,
  metric?: string,
): Promise<CsvDownload> {
  // Raw fetch (not api.get): we need the headers (filename,
  // truncation flag) alongside the blob body. A metric on an
  // intake_breakdown widget scopes the export to that tile's rows
  // (C85T0); without one the aggregate export is unchanged.
  const url =
    `/api/v1/widgets/${encodeURIComponent(widgetId)}/export.csv` +
    (metric ? `?metric=${encodeURIComponent(metric)}` : "");
  const res = await fetch(url, { credentials: "include" });
  if (!res.ok) {
    const text = await res.text();
    throw new ApiError(res.status, text || res.statusText);
  }
  const blob = await res.blob();
  return {
    blob,
    filename: exportFilename(
      res.headers.get("Content-Disposition"),
      `${widgetId}.csv`,
    ),
    truncated: res.headers.get("X-Result-Truncated") === "true",
  };
}

/** Trigger a browser download for a fetched CSV blob (filename from
 *  the server's Content-Disposition, carried by the CsvDownload). */
function downloadCsvBlob({ blob, filename }: CsvDownload): void {
  const url = URL.createObjectURL(blob);
  try {
    const a = document.createElement("a");
    a.href = url;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    a.remove();
  } finally {
    URL.revokeObjectURL(url);
  }
}

/** Download the widget's CSV export via a blob URL (filename from the
 *  server's Content-Disposition). Visible to anyone who can read the
 *  widget — the caller has already gated the affordance. An intake
 *  metric scopes the export to that tile's rows (C85T0). */
export async function downloadWidgetCsv(
  widgetId: string,
  metric?: string,
): Promise<void> {
  downloadCsvBlob(await fetchWidgetCsv(widgetId, metric));
}

/** Fetch the intake trend series as CSV (C86T0): the same series as
 *  the trend endpoint, columns
 *  bucket_start,in_app,form,email,accepted,rejected,duplicate.
 *  `granularity` is the toggle's current value; the bucket count is
 *  the server default (12 — fixed, same as the chart). */
export async function fetchWidgetTrendCsv(
  widgetId: string,
  granularity: IntakeTrendGranularity,
): Promise<CsvDownload> {
  const url =
    `/api/v1/widgets/${encodeURIComponent(widgetId)}/export.csv` +
    `?trend=1&granularity=${encodeURIComponent(granularity)}`;
  const res = await fetch(url, { credentials: "include" });
  if (!res.ok) {
    const text = await res.text();
    throw new ApiError(res.status, text || res.statusText);
  }
  const blob = await res.blob();
  return {
    blob,
    filename: exportFilename(
      res.headers.get("Content-Disposition"),
      `${widgetId}-trend.csv`,
    ),
    truncated: res.headers.get("X-Result-Truncated") === "true",
  };
}

/** Download the intake trend CSV (C86T1 menu item). Read affordance
 *  like the aggregate export — the server enforces read access. */
export async function downloadWidgetTrendCsv(
  widgetId: string,
  granularity: IntakeTrendGranularity,
): Promise<void> {
  downloadCsvBlob(await fetchWidgetTrendCsv(widgetId, granularity));
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
  metric: WidgetMetric;
  filters: WidgetFilters;
  project_ids: string[];
  max_rows: number;
  visualization: WidgetVisualization;
  gauge_color: string;
  show_percentage: boolean;
  view: WidgetVisualization;
  columns: WidgetRowColumn[];
  sort_by: WidgetRowColumn;
  sort_dir: WidgetSortDir;
} {
  const f = config.filters ?? {};
  const maxRows =
    typeof config.max_rows === "number" &&
    Number.isInteger(config.max_rows) &&
    config.max_rows >= 1 &&
    config.max_rows <= 100
      ? config.max_rows
      : 10;
  const columns =
    config.columns && config.columns.length > 0
      ? config.columns.filter((c) => ROW_COLUMNS.includes(c))
      : [...ROW_COLUMNS];
  const safeColumns = columns.length > 0 ? columns : [...ROW_COLUMNS];
  return {
    ...(config.group_by ? { group_by: config.group_by } : {}),
    columns: safeColumns,
    sort_by:
      config.sort_by && safeColumns.includes(config.sort_by)
        ? config.sort_by
        : safeColumns.includes("state")
          ? "state"
          : safeColumns[0],
    sort_dir: config.sort_dir ?? "desc",
    metric: config.metric ?? "count",
    filters: {
      assignees: f.assignees ?? [],
      priorities: f.priorities ?? [],
      states: f.states ?? [],
      labels: f.labels ?? [],
    },
    project_ids: config.project_ids ?? [],
    max_rows: maxRows,
    visualization: config.visualization ?? "grid",
    gauge_color: config.gauge_color ?? "#3b82f6",
    show_percentage: config.show_percentage ?? true,
    view: config.view ?? "grid",
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

/* ------------------------------------------------------------------ */
/* Phase-2 pure helpers (unit-tested).                                 */
/* ------------------------------------------------------------------ */

/**
 * The caller's effective access on a dashboard. The server's my_access
 * is authoritative when present; otherwise it falls back to Plane's
 * edit rule (creator → owner; workspace admin 20 on PUBLIC → edit).
 */
export function dashboardAccess(
  dashboard: Pick<Dashboard, "created_by" | "visibility" | "my_access">,
  userId: string | null | undefined,
  workspaceRole: number,
): DashboardAccess {
  if (dashboard.my_access) return dashboard.my_access;
  if (!userId) return "view";
  if (dashboard.created_by === userId) return "owner";
  return dashboard.visibility === "public" && workspaceRole >= 20
    ? "edit"
    : "view";
}

/** Only the dashboard owner manages shares, deletes, or changes
 *  visibility (C83T0 owner-only share rule). */
export function canShareDashboard(access: DashboardAccess): boolean {
  return access === "owner";
}

/** Widget editing (add/configure/move/resize) is owner + edit access;
 *  view access is strictly read-only. */
export function canEditWidgets(access: DashboardAccess): boolean {
  return access === "owner" || access === "edit";
}

/** Human label for an access level (list badges). */
export const ACCESS_LABELS: Record<DashboardAccess, string> = {
  owner: "Owner",
  view: "View",
  edit: "Edit",
};

/** Pages in a row-list widget envelope (total rows / max_rows). */
export function widgetRowPageCount(data: WidgetData | undefined): number {
  if (
    typeof data?.total !== "number" ||
    typeof data?.max_rows !== "number" ||
    data.max_rows <= 0
  ) {
    return 1;
  }
  return Math.max(1, Math.ceil(data.total / data.max_rows));
}

/** Pages in a paginated widget envelope. */
export function widgetPageCount(data: WidgetData | undefined): number {
  if (
    typeof data?.total_groups !== "number" ||
    typeof data?.max_rows !== "number" ||
    data.max_rows <= 0
  ) {
    return 1;
  }
  return Math.max(1, Math.ceil(data.total_groups / data.max_rows));
}

/**
 * SVG path for a semi-circular gauge arc: a half-circle of the given
 * radius (in a 2r×r viewBox, origin top-left) filled from the left by
 * `percentage` (0–100, clamped). percentage=100 sweeps the full half.
 */
export function semiGaugeArc(
  percentage: number,
  radius: number,
): { path: string; endX: number; endY: number } {
  const p = Math.min(100, Math.max(0, percentage));
  const theta = (Math.PI * p) / 100; // 0..π from the left point
  const endX = radius - radius * Math.cos(theta);
  const endY = radius - radius * Math.sin(theta);
  const largeArc = p > 50 ? 1 : 0;
  const path = `M 0 ${radius} A ${radius} ${radius} 0 ${largeArc} 1 ${endX} ${endY}`;
  return { path, endX, endY };
}

/** One intake_breakdown tile. `duration` renders the value through
 *  formatDuration; `honestEmpty` gets the no-ingestion tooltip
 *  instead of a bare 0. `metric` is the drill-down metric — absent
 *  only for avg-time-to-decide, the aggregate with no row list and
 *  therefore no click target (C85T1). */
export interface IntakeTile {
  key: keyof IntakeBreakdown;
  label: string;
  duration?: boolean;
  honestEmpty?: boolean;
  metric?: IntakeMetric;
}

/** Grid/table order follows the C85 prototype: the six original
 *  tiles, then the three funnel tiles, then the avg aggregate last
 *  (plain text, never clickable). */
export const INTAKE_TILES: IntakeTile[] = [
  { key: "in_app_volume", label: "In-app volume", metric: "in_app" },
  { key: "form_volume", label: "Form volume", metric: "form" },
  {
    key: "email_volume",
    label: "Email volume",
    honestEmpty: true,
    metric: "email",
  },
  { key: "total_accepted", label: "Total accepted", metric: "accepted" },
  { key: "total_rejected", label: "Total rejected", metric: "rejected" },
  { key: "total_duplicate", label: "Total duplicate", metric: "duplicate" },
  { key: "pending", label: "Pending", metric: "pending" },
  { key: "snoozed", label: "Snoozed", metric: "snoozed" },
  { key: "avg_decide_seconds", label: "Avg. time to decide", duration: true },
];

/** The honest tooltip on the email tile — glance has no email
 *  ingestion, so the tile is always 0 and must never look live. */
export const EMAIL_INGESTION_NOTE =
  "glance has no email ingestion yet — this is always 0, not a live count.";

/** Format a seconds duration compactly (90 → "1m 30s", 90000 → "1d 1h"). */
export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return "—";
  const s = Math.round(seconds);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) {
    const rem = s % 60;
    return rem === 0 ? `${m}m` : `${m}m ${rem}s`;
  }
  const h = Math.floor(m / 60);
  if (h < 24) {
    const rem = m % 60;
    return rem === 0 ? `${h}h` : `${h}h ${rem}m`;
  }
  const d = Math.floor(h / 24);
  const rem = h % 24;
  return rem === 0 ? `${d}d` : `${d}d ${rem}h`;
}

/** Gauge color fallback when the config has none (matches the backend
 *  default). */
export const DEFAULT_GAUGE_COLOR = "#3b82f6";
