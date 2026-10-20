package service

// Customizable dashboards, phases 1+2 (C81T0, C83T0): workspace-scoped
// data canvases à la Plane Dashboards — a 12-column grid of widgets,
// each independently configured (chart type, grouping, filters,
// project scope) and recomputed live from work-item data.
//
// Phase 2 adds Plane's newer widget types (smart_counter, smart_gauge,
// table_2d, intake_breakdown), intake source tracking (in_app|form|
// email on intake_issues), per-member dashboard sharing (owner-managed,
// View/Edit access), a static template catalog with create-from-
// template, and per-widget CSV export.
//
// AuthZ:
//   - Reads (list/get/widget data/export.csv): workspace member (15)+;
//     guests (5) get ErrForbidden, bad slug / non-member ErrNotFound.
//   - Visibility: public dashboards are visible to all members; a
//     private dashboard is visible only to its creator and to members
//     holding a share row — any other caller gets ErrNotFound (never
//     a 403 that would leak existence).
//   - Effective access per caller: "owner" (creator), "edit"/"view"
//     (share row), else on public dashboards "edit" for workspace
//     admins (20) / "view" for members (phase-1 rule); on private
//     dashboards without a share: none (ErrNotFound).
//   - Edit ("edit" or "owner"): widget CRUD, layout PATCH, dashboard
//     PATCH of name/description. Anything less → ErrForbidden (the
//     caller CAN see the dashboard, so 403 is safe).
//   - Owner-only: delete dashboard, change visibility, manage shares.
//     Everyone else — admins and edit-shared members included — gets
//     ErrForbidden.
//   - Malformed UUIDs never reach the service: the API layer 400s them
//     first (requireUUIDParam); the service still guards with
//     isUUIDFormat where it parses stored/filter input.
//
// Config validation (honest 400s at write time):
//   - widget_type ∈ bar|line|area|donut|pie|number|smart_counter|
//     smart_gauge|table_2d|intake_breakdown (also a DB CHECK).
//   - group_by ∈ state|priority|assignee|label|state_group|project|
//     created_by — required for the chart and phase-2 grouped types,
//     optional and ignored for number; ignored (not required) for
//     intake_breakdown.
//   - metric ∈ count|estimate_points (default count) for
//     smart_counter|smart_gauge; reserved to "count" for the phase-1
//     chart types and number; accepted-but-ignored for table_2d (its
//     metric is fixed to count — Plane parity) and intake_breakdown.
//   - smart_counter: max_rows 1..100 (default 10),
//     visualization ∈ grid|table (default grid).
//   - smart_gauge: max_rows 1..100 (default 10), gauge_color #rrggbb
//     (default #3b82f6), show_percentage bool (default true).
//   - table_2d: max_rows 1..100 (default 10).
//   - intake_breakdown: view ∈ grid|table (default grid). Widget-level
//     filters AND project_ids are ignored — it honors the dashboard's
//     project scope (every non-archived project of the workspace) only.
//   - filters.{assignees,states,labels} are UUID arrays;
//     filters.priorities are ints 0..4; unknown keys are rejected.
//   - project_ids is a UUID array; unknown keys are rejected. Keys that
//     are meaningless for a widget's type (e.g. max_rows on a bar
//     chart) are rejected — each type documents its own key set.
//   - grid_w/grid_h are clamped to 1..12, grid_x/grid_y to ≥ 0 (never
//     an error — the canvas just clamps).
//
// Widget data is computed LIVE from the issues table: live issues
// (deleted_at IS NULL, archived_at IS NULL — the issue list's default
// scope) in the dashboard's workspace, narrowed to the widget's
// project_ids (empty = every non-archived project of the workspace —
// glance has no per-project private visibility; ListProjects uses the
// same non-archived default) ANDed with config.filters, grouped by
// config.group_by. Group keys are the human labels the frontend
// renders: state name, priority label (None/Low/Medium/High/Urgent —
// the web/src/lib/types.ts PRIORITY_LABELS order), assignee display
// name (COALESCE(NULLIF(name,''), email)), label name, state-group
// display name (Triage/Backlog/Unstarted/Started/Completed/Cancelled),
// project name, creator display name. Same-named buckets across
// projects merge (documented per-widget-type behavior). Assignee
// grouping adds an "Unassigned" bucket and label grouping a "No label"
// bucket so the groups partition every counted issue.
//
// Smart gauge completion = the share of the group sitting in a
// 'completed'-group state. With metric=estimate_points it is the sum
// of completed-group points over the sum of all points; when nothing
// is estimated the percentage is 0 (Plane parity — estimates are
// optional in glance).

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Dashboard visibility values (also a DB CHECK on dashboards).
const (
	DashboardVisibilityPublic  = "public"
	DashboardVisibilityPrivate = "private"
)

// Widget types (also a DB CHECK on dashboard_widgets).
const (
	WidgetTypeBar          = "bar"
	WidgetTypeLine         = "line"
	WidgetTypeArea         = "area"
	WidgetTypeDonut        = "donut"
	WidgetTypePie          = "pie"
	WidgetTypeNumber       = "number"
	WidgetTypeSmartCounter = "smart_counter"
	WidgetTypeSmartGauge   = "smart_gauge"
	WidgetTypeTable2D      = "table_2d"
	WidgetTypeIntake       = "intake_breakdown"
)

// Widget group-by dimensions.
const (
	WidgetGroupByState      = "state"
	WidgetGroupByPriority   = "priority"
	WidgetGroupByAssignee   = "assignee"
	WidgetGroupByLabel      = "label"
	WidgetGroupByStateGroup = "state_group"
	WidgetGroupByProject    = "project"
	WidgetGroupByCreatedBy  = "created_by"
)

// Widget metrics.
const (
	WidgetMetricCount          = "count"
	WidgetMetricEstimatePoints = "estimate_points"
)

// Dashboard access levels: the caller's effective standing on one
// dashboard (my_access in the list/detail responses).
const (
	DashboardAccessOwner = "owner"
	DashboardAccessEdit  = "edit"
	DashboardAccessView  = "view"
	DashboardAccessNone  = "none"
)

// Dashboard share access values (also a DB CHECK on dashboard_shares).
const (
	DashboardShareView = "view"
	DashboardShareEdit = "edit"
)

var (
	// ErrInvalidVisibility is returned when a dashboard visibility is
	// not "public"|"private". The handler maps it to 400 bad_request.
	ErrInvalidVisibility = errors.New("service: invalid dashboard visibility: want public or private")
	// ErrDashboardNotFound is a confirmed-missing dashboard (the caller
	// passed the workspace/visibility gates, so the 404 message can name
	// it). Private dashboards of other creators surface ErrNotFound
	// instead — never leak existence.
	ErrDashboardNotFound = errors.New("service: dashboard not found")
	// ErrWidgetNotFound is a confirmed-missing widget, or a widget on a
	// private dashboard the caller may not see.
	ErrWidgetNotFound = errors.New("service: widget not found")
	// ErrInvalidWidgetType is returned when widget_type is not one of
	// the ten chart types. The handler maps it to 400 bad_request.
	ErrInvalidWidgetType = errors.New("service: invalid widget_type")
	// ErrInvalidWidgetConfig is returned when the widget config JSON
	// fails validation (bad group_by, bad metric, bad max_rows,
	// malformed filter/project_ids values, unknown keys, keys that are
	// meaningless for the widget's type). 400 bad_request.
	ErrInvalidWidgetConfig = errors.New("service: invalid widget config")
	// ErrInvalidWidgetPage is returned when the widget-data page query param
	// is not an integer >= 1. The handler maps it to 400 bad_request.
	ErrInvalidWidgetPage = errors.New("service: invalid page: want an integer >= 1")
	// ErrInvalidDashboardShare is returned when a dashboard share entry fails
	// validation (bad member, guest, duplicate, bad access value). The
	// handler maps it to 400 bad_request.
	ErrInvalidDashboardShare = errors.New("service: invalid dashboard share")
	// ErrUnknownDashboardTemplate is returned when a create-from-
	// template references a template id outside the static catalog. The
	// handler maps it to 400 bad_request.
	ErrUnknownDashboardTemplate = errors.New("service: unknown dashboard template")
)

// priorityLabels mirrors web/src/lib/types.ts PRIORITY_LABELS — the
// human labels the frontend renders for priority buckets.
var priorityLabels = []string{"None", "Low", "Medium", "High", "Urgent"}

// Dashboard is a workspace-scoped widget canvas. Widgets ride along on
// the detail read only (the list read omits them); the frontend fetches
// per-widget data separately via GetWidgetData. SharedWithMe,
// MyAccess, and Shared are the caller's sharing standing: SharedWithMe
// is true when the caller holds a share row on someone else's
// dashboard; MyAccess is the effective access (owner|view|edit) the
// frontend gates affordances on; Shared is true on dashboards the
// caller owns that have at least one share (feeds the Shared tab).
type Dashboard struct {
	ID            string    `json:"id"`
	WorkspaceID   string    `json:"workspace_id"`
	Name          string    `json:"name"`
	Description   *string   `json:"description,omitempty"`
	Visibility    string    `json:"visibility"`
	CreatedBy     string    `json:"created_by"`
	CreatedByName string    `json:"created_by_name"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	Widgets       []Widget  `json:"widgets"`
	SharedWithMe  bool      `json:"shared_with_me"`
	MyAccess      string    `json:"my_access"`
	Shared        bool      `json:"shared"`
}

// Widget is one configured visualization on a dashboard canvas.
type Widget struct {
	ID          string          `json:"id"`
	DashboardID string          `json:"dashboard_id"`
	Title       string          `json:"title"`
	WidgetType  string          `json:"widget_type"`
	Config      json.RawMessage `json:"config"`
	GridX       int             `json:"grid_x"`
	GridY       int             `json:"grid_y"`
	GridW       int             `json:"grid_w"`
	GridH       int             `json:"grid_h"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// WidgetDataGroup is one bucket of a grouped widget. The key IS the
// human label the frontend renders (state name, priority label,
// assignee display name, label name, state-group display name,
// project name, creator display name) — same-named buckets across
// projects merge. "Unassigned" (assignee) and "No label" (label) are
// synthetic buckets so the groups partition every counted issue.
// Completed and Percentage are set only for smart_gauge widgets:
// completed is the completed share of the group (count of issues in a
// 'completed'-group state, or sum of their estimate points when
// metric=estimate_points) and percentage is 100*completed/value (0
// when value is 0 — e.g. nothing estimated).
type WidgetDataGroup struct {
	Key        string   `json:"key"`
	Label      string   `json:"label"`
	Value      int      `json:"value"`
	Completed  *int     `json:"completed,omitempty"`
	Percentage *float64 `json:"percentage,omitempty"`
}

// IntakeBreakdown is the six-number intake health summary of the
// intake_breakdown widget: in-app vs form vs email intake volume,
// terminal triage outcomes, and the mean time from intake-row creation
// to a terminal decision (rejected/accepted/duplicate), in seconds.
// EmailVolume is ALWAYS 0: glance has no email ingestion, and the
// frontend renders the tile with an honest "no email ingestion yet"
// tooltip rather than a fake number.
type IntakeBreakdown struct {
	InAppVolume      int     `json:"in_app_volume"`
	EmailVolume      int     `json:"email_volume"`
	FormVolume       int     `json:"form_volume"`
	TotalAccepted    int     `json:"total_accepted"`
	TotalRejected    int     `json:"total_rejected"`
	AvgDecideSeconds float64 `json:"avg_decide_seconds"`
}

// WidgetData is the live recompute result: groups for the chart and
// phase-2 grouped types, a scalar value for number widgets, the intake
// summary for intake_breakdown widgets. Paginated types
// (smart_counter, smart_gauge, table_2d) also carry page, max_rows, and
// total_groups; groups holds only the requested page.
type WidgetData struct {
	Groups      []WidgetDataGroup `json:"groups,omitempty"`
	Value       *int              `json:"value,omitempty"`
	Page        *int              `json:"page,omitempty"`
	MaxRows     *int              `json:"max_rows,omitempty"`
	TotalGroups *int              `json:"total_groups,omitempty"`
	Intake      *IntakeBreakdown  `json:"intake,omitempty"`
}

// dashboardRow is the internal dashboard row plus the creator check.
type dashboardRow struct {
	Dashboard
	isCreator bool
	role      int
}

// validWidgetType reports whether t is one of the ten widget types.
func validWidgetType(t string) bool {
	switch t {
	case WidgetTypeBar, WidgetTypeLine, WidgetTypeArea,
		WidgetTypeDonut, WidgetTypePie, WidgetTypeNumber,
		WidgetTypeSmartCounter, WidgetTypeSmartGauge,
		WidgetTypeTable2D, WidgetTypeIntake:
		return true
	}
	return false
}

// validGroupBy reports whether g is a supported grouping dimension.
func validGroupBy(g string) bool {
	switch g {
	case WidgetGroupByState, WidgetGroupByPriority, WidgetGroupByAssignee,
		WidgetGroupByLabel, WidgetGroupByStateGroup, WidgetGroupByProject,
		WidgetGroupByCreatedBy:
		return true
	}
	return false
}

// validMetric reports whether m is a supported widget metric.
func validMetric(m string) bool {
	return m == WidgetMetricCount || m == WidgetMetricEstimatePoints
}

// stateGroupLabels maps states."group" slugs to the display names the
// frontend renders for state_group buckets.
var stateGroupLabels = map[string]string{
	"triage":    "Triage",
	"backlog":   "Backlog",
	"unstarted": "Unstarted",
	"started":   "Started",
	"completed": "Completed",
	"cancelled": "Cancelled",
}

// stateGroupOrder is the canonical display order of state groups
// (mirrors validStateGroups in state.go).
var stateGroupOrder = []string{"triage", "backlog", "unstarted", "started", "completed", "cancelled"}

// widgetConfig is the validated, normalized form of a widget's config
// JSON. Filters are ANDed; an empty projectIDs means "every
// non-archived project of the workspace". Phase-2 fields carry their
// defaults when absent: maxRows 10, visualization "grid",
// gaugeColor "#3b82f6", showPercentage true, view "grid".
type widgetConfig struct {
	groupBy        string
	metric         string
	assignees      []string
	priorities     []int
	states         []string
	labels         []string
	projectIDs     []string
	maxRows        int
	visualization  string
	gaugeColor     string
	showPercentage bool
	view           string
}

// hexColor reports whether s is a #rrggbb hex color.
func hexColor(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, c := range s[1:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// parseWidgetConfig validates raw config JSON for the given widget type
// and returns its normalized form. Unknown keys, bad enums, malformed
// UUIDs, out-of-range priorities, and keys that are meaningless for the
// widget's type are all ErrInvalidWidgetConfig. Defaults are applied
// for the phase-2 presentation keys (max_rows 10, visualization/view
// "grid", gauge_color "#3b82f6", show_percentage true).
func parseWidgetConfig(widgetType string, raw json.RawMessage) (*widgetConfig, error) {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, ErrInvalidWidgetConfig
	}
	for k := range top {
		switch k {
		case "group_by", "metric", "filters", "project_ids",
			"max_rows", "visualization", "gauge_color", "show_percentage", "view":
		default:
			return nil, fmt.Errorf("%w: unknown config key %q", ErrInvalidWidgetConfig, k)
		}
	}
	cfg := &widgetConfig{
		metric:         WidgetMetricCount,
		maxRows:        10,
		visualization:  "grid",
		gaugeColor:     "#3b82f6",
		showPercentage: true,
		view:           "grid",
	}

	// Each widget type documents its own key set; a key that exists but
	// is meaningless for the type is a 400 (honest config surface).
	allow := func(key string, types ...string) error {
		for _, t := range types {
			if t == widgetType {
				return nil
			}
		}
		return fmt.Errorf("%w: %q is not supported for %q widgets", ErrInvalidWidgetConfig, key, widgetType)
	}
	charts := []string{WidgetTypeBar, WidgetTypeLine, WidgetTypeArea, WidgetTypeDonut, WidgetTypePie}
	grouped := append(append([]string{}, charts...),
		WidgetTypeSmartCounter, WidgetTypeSmartGauge, WidgetTypeTable2D)
	// metric is a known key on every type: charts/number/counter/gauge
	// validate its value; table_2d and intake_breakdown accept and
	// ignore it (their metric is fixed/meaningless — Plane parity).
	metricTypes := append(append([]string{}, charts...),
		WidgetTypeNumber, WidgetTypeSmartCounter, WidgetTypeSmartGauge,
		WidgetTypeTable2D, WidgetTypeIntake)
	for k := range top {
		switch k {
		case "group_by", "filters", "project_ids":
			// Accepted on every type; group_by is ignored (not
			// required) for number and intake_breakdown.
		case "metric":
			if err := allow(k, metricTypes...); err != nil {
				return nil, err
			}
		case "max_rows":
			if err := allow(k, WidgetTypeSmartCounter, WidgetTypeSmartGauge, WidgetTypeTable2D); err != nil {
				return nil, err
			}
		case "visualization":
			if err := allow(k, WidgetTypeSmartCounter); err != nil {
				return nil, err
			}
		case "gauge_color", "show_percentage":
			if err := allow(k, WidgetTypeSmartGauge); err != nil {
				return nil, err
			}
		case "view":
			if err := allow(k, WidgetTypeIntake); err != nil {
				return nil, err
			}
		}
	}

	if rawGB, ok := top["group_by"]; ok {
		var gb string
		if err := json.Unmarshal(rawGB, &gb); err != nil || !validGroupBy(gb) {
			return nil, fmt.Errorf("%w: group_by must be one of state|priority|assignee|label|state_group|project|created_by", ErrInvalidWidgetConfig)
		}
		cfg.groupBy = gb
	}
	if rawM, ok := top["metric"]; ok {
		var m string
		if err := json.Unmarshal(rawM, &m); err != nil {
			return nil, fmt.Errorf("%w: metric must be a string", ErrInvalidWidgetConfig)
		}
		switch widgetType {
		case WidgetTypeSmartCounter, WidgetTypeSmartGauge:
			if !validMetric(m) {
				return nil, fmt.Errorf("%w: metric must be one of count|estimate_points", ErrInvalidWidgetConfig)
			}
			cfg.metric = m
		case WidgetTypeTable2D, WidgetTypeIntake:
			// Fixed to count (table_2d) / meaningless (intake):
			// accepted and ignored with a note, like phase 1's
			// number widget ignored group_by. Any string is
			// tolerated; non-strings were rejected above.
		default:
			if m != WidgetMetricCount {
				return nil, fmt.Errorf("%w: metric is reserved: only \"count\" is supported for %q widgets", ErrInvalidWidgetConfig, widgetType)
			}
			cfg.metric = m
		}
	}
	if rawF, ok := top["filters"]; ok {
		var f map[string]json.RawMessage
		if err := json.Unmarshal(rawF, &f); err != nil {
			return nil, fmt.Errorf("%w: filters must be an object", ErrInvalidWidgetConfig)
		}
		for k := range f {
			switch k {
			case "assignees", "priorities", "states", "labels":
			default:
				return nil, fmt.Errorf("%w: unknown filters key %q", ErrInvalidWidgetConfig, k)
			}
		}
		uuidList := func(key string) ([]string, error) {
			rawV, ok := f[key]
			if !ok {
				return nil, nil
			}
			var vals []string
			if err := json.Unmarshal(rawV, &vals); err != nil {
				return nil, fmt.Errorf("%w: filters.%s must be an array of UUID strings", ErrInvalidWidgetConfig, key)
			}
			for _, v := range vals {
				if !isUUIDFormat(v) {
					return nil, fmt.Errorf("%w: filters.%s contains a malformed UUID", ErrInvalidWidgetConfig, key)
				}
			}
			return vals, nil
		}
		var err error
		if cfg.assignees, err = uuidList("assignees"); err != nil {
			return nil, err
		}
		if cfg.states, err = uuidList("states"); err != nil {
			return nil, err
		}
		if cfg.labels, err = uuidList("labels"); err != nil {
			return nil, err
		}
		if rawP, ok := f["priorities"]; ok {
			var nums []float64
			if err := json.Unmarshal(rawP, &nums); err != nil {
				return nil, fmt.Errorf("%w: filters.priorities must be an array of integers 0-4", ErrInvalidWidgetConfig)
			}
			for _, n := range nums {
				if n != float64(int(n)) || n < 0 || n > 4 {
					return nil, fmt.Errorf("%w: filters.priorities must be integers 0-4", ErrInvalidWidgetConfig)
				}
				cfg.priorities = append(cfg.priorities, int(n))
			}
		}
	}
	if rawP, ok := top["project_ids"]; ok {
		var ids []string
		if err := json.Unmarshal(rawP, &ids); err != nil {
			return nil, fmt.Errorf("%w: project_ids must be an array of UUID strings", ErrInvalidWidgetConfig)
		}
		for _, id := range ids {
			if !isUUIDFormat(id) {
				return nil, fmt.Errorf("%w: project_ids contains a malformed UUID", ErrInvalidWidgetConfig)
			}
		}
		cfg.projectIDs = ids
	}
	if rawMR, ok := top["max_rows"]; ok {
		var n float64
		if err := json.Unmarshal(rawMR, &n); err != nil || n != float64(int(n)) || n < 1 || n > 100 {
			return nil, fmt.Errorf("%w: max_rows must be an integer 1-100", ErrInvalidWidgetConfig)
		}
		cfg.maxRows = int(n)
	}
	if rawV, ok := top["visualization"]; ok {
		var v string
		if err := json.Unmarshal(rawV, &v); err != nil || (v != "grid" && v != "table") {
			return nil, fmt.Errorf("%w: visualization must be one of grid|table", ErrInvalidWidgetConfig)
		}
		cfg.visualization = v
	}
	if rawC, ok := top["gauge_color"]; ok {
		var c string
		if err := json.Unmarshal(rawC, &c); err != nil || !hexColor(c) {
			return nil, fmt.Errorf("%w: gauge_color must be a #rrggbb hex color", ErrInvalidWidgetConfig)
		}
		cfg.gaugeColor = c
	}
	if rawS, ok := top["show_percentage"]; ok {
		var b bool
		if err := json.Unmarshal(rawS, &b); err != nil {
			return nil, fmt.Errorf("%w: show_percentage must be a boolean", ErrInvalidWidgetConfig)
		}
		cfg.showPercentage = b
	}
	if rawV, ok := top["view"]; ok {
		var v string
		if err := json.Unmarshal(rawV, &v); err != nil || (v != "grid" && v != "table") {
			return nil, fmt.Errorf("%w: view must be one of grid|table", ErrInvalidWidgetConfig)
		}
		cfg.view = v
	}

	switch widgetType {
	case WidgetTypeNumber:
		// group_by is meaningless for a scalar; ignore it rather than
		// rejecting a stale config when the type flips to number.
		cfg.groupBy = ""
	case WidgetTypeIntake:
		// No group_by, no metric: the six headline numbers are
		// computed over the dashboard's project scope only.
		// Widget-level filters AND project_ids are ignored (Plane
		// parity — the breakdown is never narrowed per widget).
		cfg.groupBy = ""
		cfg.assignees, cfg.priorities, cfg.states, cfg.labels = nil, nil, nil, nil
		cfg.projectIDs = nil
	default:
		if cfg.groupBy == "" {
			for _, t := range grouped {
				if t == widgetType {
					return nil, fmt.Errorf("%w: group_by is required for %q widgets", ErrInvalidWidgetConfig, widgetType)
				}
			}
		}
	}
	return cfg, nil
}

// clampGrid clamps grid units to the 12-column canvas: w/h to 1..12,
// x/y to non-negative. Clamping, never an error.
func clampGrid(x, y, w, h int) (int, int, int, int) {
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if w < 1 {
		w = 1
	} else if w > 12 {
		w = 12
	}
	if h < 1 {
		h = 1
	} else if h > 12 {
		h = 12
	}
	return x, y, w, h
}

// resolveDashboardWorkspace resolves the caller's membership of the
// workspace: bad slug / non-member → ErrNotFound (shared 404), role <
// member (15) → ErrForbidden.
func resolveDashboardWorkspace(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID string) (string, int, error) {
	wsID, role, err := workspaceIDForActor(pool.QueryRow(ctx,
		`SELECT w.id::text, m.role
		   FROM workspaces w
		   JOIN workspace_members m ON m.workspace_id = w.id
		  WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID))
	if err != nil {
		return "", 0, err
	}
	if role < RoleMember {
		return "", 0, ErrForbidden
	}
	return wsID, role, nil
}

const dashboardColumns = `d.id::text, d.workspace_id::text, d.name, d.description, d.visibility,
	d.created_by::text, COALESCE(NULLIF(u.name, ''), u.email), d.created_at, d.updated_at`

// scanDashboard scans a dashboard row (dashboardColumns order) plus the
// trailing is_creator boolean when selected.
func scanDashboard(row pgx.Row, withCreatorFlag bool) (dashboardRow, error) {
	var dr dashboardRow
	d := &dr.Dashboard
	if withCreatorFlag {
		err := row.Scan(&d.ID, &d.WorkspaceID, &d.Name, &d.Description, &d.Visibility,
			&d.CreatedBy, &d.CreatedByName, &d.CreatedAt, &d.UpdatedAt, &dr.isCreator)
		return dr, err
	}
	err := row.Scan(&d.ID, &d.WorkspaceID, &d.Name, &d.Description, &d.Visibility,
		&d.CreatedBy, &d.CreatedByName, &d.CreatedAt, &d.UpdatedAt)
	return dr, err
}

// dashboardAccessOf computes the caller's effective access level on a
// dashboard: the creator is "owner"; a share row grants exactly its
// access ("view"|"edit"); otherwise a public dashboard is "edit" for
// workspace admins (20) and "view" for members (the phase-1 rule), and
// a private dashboard is "none" (invisible — the caller gets
// ErrNotFound, never a 403 that would leak existence).
func dashboardAccessOf(visibility, share string, role int) string {
	switch share {
	case DashboardShareEdit:
		return DashboardAccessEdit
	case DashboardShareView:
		return DashboardAccessView
	}
	if visibility == DashboardVisibilityPublic {
		if role >= RoleAdmin {
			return DashboardAccessEdit
		}
		return DashboardAccessView
	}
	return DashboardAccessNone
}

// shareAccessOf returns the caller's share access on the dashboard, ""
// when no share row exists.
func shareAccessOf(ctx context.Context, pool *pgxpool.Pool, dashboardID, actorID string) (string, error) {
	var access string
	err := pool.QueryRow(ctx,
		`SELECT access FROM dashboard_shares WHERE dashboard_id = $1::uuid AND member_id = $2::uuid`,
		dashboardID, actorID).Scan(&access)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return access, nil
}

// resolveDashboardAccess loads the dashboard row and resolves the
// caller's effective access (owner|edit|view|none). A confirmed-missing
// dashboard is ErrDashboardNotFound.
func resolveDashboardAccess(ctx context.Context, pool *pgxpool.Pool, wsID string, role int, dashboardID, actorID string) (dashboardRow, string, error) {
	dr, err := scanDashboard(pool.QueryRow(ctx,
		`SELECT `+dashboardColumns+`, d.created_by = $3::uuid
		   FROM dashboards d
		   JOIN users u ON u.id = d.created_by
		  WHERE d.id = $1::uuid AND d.workspace_id = $2::uuid`,
		dashboardID, wsID, actorID), true)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dashboardRow{}, "", ErrDashboardNotFound
		}
		return dashboardRow{}, "", err
	}
	dr.role = role
	if dr.isCreator {
		return dr, DashboardAccessOwner, nil
	}
	share, err := shareAccessOf(ctx, pool, dashboardID, actorID)
	if err != nil {
		return dashboardRow{}, "", err
	}
	return dr, dashboardAccessOf(dr.Dashboard.Visibility, share, role), nil
}

// loadDashboard fetches the dashboard and enforces the visibility + edit
// rules. edit=false requires read access (owner|edit|view); a private
// dashboard invisible to the caller is ErrNotFound (never a leak).
// edit=true additionally requires the edit level (owner or edit-share,
// or admin on public dashboards without a share) — anything less is
// ErrForbidden.
func loadDashboard(ctx context.Context, pool *pgxpool.Pool, wsID string, role int, dashboardID, actorID string, edit bool) (dashboardRow, error) {
	dr, access, err := resolveDashboardAccess(ctx, pool, wsID, role, dashboardID, actorID)
	if err != nil {
		return dashboardRow{}, err
	}
	if access == DashboardAccessNone {
		return dashboardRow{}, ErrNotFound
	}
	if edit && access != DashboardAccessOwner && access != DashboardAccessEdit {
		return dashboardRow{}, ErrForbidden
	}
	return dr, nil
}

// loadDashboardOwner is the owner-only gate: delete dashboard, change
// visibility, manage shares. Everyone else — admins and edit-shared
// members included — gets ErrForbidden; an invisible dashboard is
// ErrNotFound.
func loadDashboardOwner(ctx context.Context, pool *pgxpool.Pool, wsID string, role int, dashboardID, actorID string) (dashboardRow, error) {
	dr, access, err := resolveDashboardAccess(ctx, pool, wsID, role, dashboardID, actorID)
	if err != nil {
		return dashboardRow{}, err
	}
	if access == DashboardAccessNone {
		return dashboardRow{}, ErrNotFound
	}
	if access != DashboardAccessOwner {
		return dashboardRow{}, ErrForbidden
	}
	return dr, nil
}

const widgetColumns = `w.id::text, w.dashboard_id::text, w.title, w.widget_type, w.config,
	w.grid_x, w.grid_y, w.grid_w, w.grid_h, w.created_at, w.updated_at`

func scanWidget(row pgx.Row) (Widget, error) {
	var wd Widget
	err := row.Scan(&wd.ID, &wd.DashboardID, &wd.Title, &wd.WidgetType, &wd.Config,
		&wd.GridX, &wd.GridY, &wd.GridW, &wd.GridH, &wd.CreatedAt, &wd.UpdatedAt)
	return wd, err
}

// selectDashboardRow re-reads a dashboard row with the creator display
// name (no authZ gates — the caller already passed them).
func selectDashboardRow(ctx context.Context, pool *pgxpool.Pool, dashboardID, actorID string) (dashboardRow, error) {
	return scanDashboard(pool.QueryRow(ctx,
		`SELECT `+dashboardColumns+`, d.created_by = $2::uuid
		   FROM dashboards d
		   JOIN users u ON u.id = d.created_by
		  WHERE d.id = $1::uuid`,
		dashboardID, actorID), true)
}

// AuthorizeWidgetWrite resolves the widget and enforces the dashboard's
// edit rule, returning the widget. Handlers that must validate the
// request body only after authZ (so a 400 can never leak a private
// widget's existence to a non-creator) call this before touching the
// body.
func AuthorizeWidgetWrite(ctx context.Context, pool *pgxpool.Pool, widgetID, actorID string) (*Widget, error) {
	_, wd, err := loadWidgetDashboard(ctx, pool, widgetID, actorID, true)
	if err != nil {
		return nil, err
	}
	return &wd, nil
}

// loadWidgetDashboard resolves a widget through its dashboard to the
// workspace, enforcing the same visibility/edit rules as the dashboard
// itself. Widget routes are top-level (/api/v1/widgets/…), so tenancy
// comes from the join, not a path slug.
func loadWidgetDashboard(ctx context.Context, pool *pgxpool.Pool, widgetID, actorID string, edit bool) (dashboardRow, Widget, error) {
	var wd Widget
	var wsID string
	err := pool.QueryRow(ctx,
		`SELECT `+widgetColumns+`, d.workspace_id::text
		   FROM dashboard_widgets w
		   JOIN dashboards d ON d.id = w.dashboard_id
		  WHERE w.id = $1::uuid`,
		widgetID).Scan(&wd.ID, &wd.DashboardID, &wd.Title, &wd.WidgetType, &wd.Config,
		&wd.GridX, &wd.GridY, &wd.GridW, &wd.GridH, &wd.CreatedAt, &wd.UpdatedAt, &wsID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dashboardRow{}, Widget{}, ErrWidgetNotFound
		}
		return dashboardRow{}, Widget{}, err
	}
	var role int
	err = pool.QueryRow(ctx,
		`SELECT m.role FROM workspace_members m WHERE m.workspace_id = $1::uuid AND m.user_id = $2::uuid`,
		wsID, actorID).Scan(&role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dashboardRow{}, Widget{}, ErrNotFound
		}
		return dashboardRow{}, Widget{}, err
	}
	if role < RoleMember {
		return dashboardRow{}, Widget{}, ErrForbidden
	}
	dr, err := loadDashboard(ctx, pool, wsID, role, wd.DashboardID, actorID, edit)
	if err != nil {
		// A private dashboard the caller may not see must read as a
		// missing widget, not a missing dashboard (no existence leak
		// through the widget path).
		if errors.Is(err, ErrNotFound) {
			return dashboardRow{}, Widget{}, ErrWidgetNotFound
		}
		return dashboardRow{}, Widget{}, err
	}
	return dr, wd, nil
}

// CreateDashboard creates a dashboard in the workspace. Member (15)+;
// visibility defaults to public; anything else is
// ErrInvalidVisibility. Name is required (ErrNameRequired).
func CreateDashboard(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, name string, description *string, visibility string) (*Dashboard, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrNameRequired
	}
	if visibility == "" {
		visibility = DashboardVisibilityPublic
	}
	if visibility != DashboardVisibilityPublic && visibility != DashboardVisibilityPrivate {
		return nil, ErrInvalidVisibility
	}
	wsID, _, err := resolveDashboardWorkspace(ctx, pool, wsSlug, actorID)
	if err != nil {
		return nil, err
	}
	var dashboardID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO dashboards (workspace_id, name, description, visibility, created_by)
		 VALUES ($1::uuid, $2, $3, $4, $5::uuid)
		 RETURNING id::text`,
		wsID, name, description, visibility, actorID).Scan(&dashboardID); err != nil {
		return nil, err
	}
	dr, err := selectDashboardRow(ctx, pool, dashboardID, actorID)
	if err != nil {
		return nil, err
	}
	out := dr.Dashboard
	return &out, nil
}

// populateDashboardAccess fills the caller's sharing standing on a
// dashboard row: SharedWithMe (a share row on someone else's
// dashboard), MyAccess (owner|view|edit — the effective access the
// frontend gates affordances on), and Shared (owned by the caller AND
// shared out to at least one member — feeds the Shared tab).
func populateDashboardAccess(d *Dashboard, isCreator bool, share string, role int, hasShares bool) {
	switch {
	case isCreator:
		d.MyAccess = DashboardAccessOwner
		d.Shared = hasShares
	case share != "":
		d.SharedWithMe = true
		d.MyAccess = share
	default:
		// No share row: public dashboards fall back to the phase-1
		// rule (admins edit, members view). Private dashboards never
		// reach here without a share row.
		if d.Visibility == DashboardVisibilityPublic && role >= RoleAdmin {
			d.MyAccess = DashboardAccessEdit
		} else {
			d.MyAccess = DashboardAccessView
		}
	}
}

// ListDashboards returns the workspace's dashboards visible to the
// caller: public ones, the caller's own private ones, and private ones
// shared with the caller (view or edit), newest updated first. Member
// (15)+; guests ErrForbidden. Widgets are NOT included (the detail
// read carries them). Each row carries shared_with_me, my_access, and
// the shared indicator for dashboards the caller owns and shares out.
func ListDashboards(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID string) ([]Dashboard, error) {
	wsID, role, err := resolveDashboardWorkspace(ctx, pool, wsSlug, actorID)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT `+dashboardColumns+`, d.created_by = $2::uuid, s.access,
		        EXISTS (SELECT 1 FROM dashboard_shares x WHERE x.dashboard_id = d.id)
		   FROM dashboards d
		   JOIN users u ON u.id = d.created_by
		   LEFT JOIN dashboard_shares s ON s.dashboard_id = d.id AND s.member_id = $2::uuid
		  WHERE d.workspace_id = $1::uuid
		    AND (d.visibility = 'public' OR d.created_by = $2::uuid OR s.member_id IS NOT NULL)
		  ORDER BY d.updated_at DESC, d.id DESC`,
		wsID, actorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Dashboard{}
	for rows.Next() {
		var d Dashboard
		var isCreator bool
		var share *string
		var hasShares bool
		// dashboardColumns order, then the is_creator flag, the
		// caller's share access (nullable), and the has-shares flag.
		err := rows.Scan(&d.ID, &d.WorkspaceID, &d.Name, &d.Description, &d.Visibility,
			&d.CreatedBy, &d.CreatedByName, &d.CreatedAt, &d.UpdatedAt,
			&isCreator, &share, &hasShares)
		if err != nil {
			return nil, err
		}
		shareAccess := ""
		if share != nil {
			shareAccess = *share
		}
		populateDashboardAccess(&d, isCreator, shareAccess, role, hasShares)
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetDashboard returns one dashboard with its widgets (config only —
// data is fetched per widget via GetWidgetData). Private dashboards of
// another creator are ErrNotFound unless the caller holds a share row.
// The response carries the caller's sharing standing (shared_with_me,
// my_access, shared) for affordance gating.
func GetDashboard(ctx context.Context, pool *pgxpool.Pool, wsSlug, dashboardID, actorID string) (*Dashboard, error) {
	wsID, role, err := resolveDashboardWorkspace(ctx, pool, wsSlug, actorID)
	if err != nil {
		return nil, err
	}
	dr, access, err := resolveDashboardAccess(ctx, pool, wsID, role, dashboardID, actorID)
	if err != nil {
		return nil, err
	}
	if access == DashboardAccessNone {
		return nil, ErrNotFound
	}
	widgets, err := listWidgets(ctx, pool, dr.Dashboard.ID)
	if err != nil {
		return nil, err
	}
	var share string
	var hasShares bool
	if !dr.isCreator {
		share, err = shareAccessOf(ctx, pool, dashboardID, actorID)
		if err != nil {
			return nil, err
		}
	} else {
		err = pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM dashboard_shares WHERE dashboard_id = $1::uuid)`,
			dashboardID).Scan(&hasShares)
		if err != nil {
			return nil, err
		}
	}
	out := dr.Dashboard
	out.Widgets = widgets
	populateDashboardAccess(&out, dr.isCreator, share, role, hasShares)
	return &out, nil
}

// UpdateDashboardInput is the PATCH body: every field optional.
type UpdateDashboardInput struct {
	Name        *string
	Description *string
	Visibility  *string
}

// UpdateDashboard applies a partial update. Name/description need the
// edit level (owner, edit-share, or admin on public dashboards);
// changing visibility is owner-only (everyone else gets 403, never a
// leak — the caller can already see the dashboard). Private dashboards
// invisible to the caller are ErrNotFound.
func UpdateDashboard(ctx context.Context, pool *pgxpool.Pool, wsSlug, dashboardID, actorID string, in UpdateDashboardInput) (*Dashboard, error) {
	wsID, role, err := resolveDashboardWorkspace(ctx, pool, wsSlug, actorID)
	if err != nil {
		return nil, err
	}
	var dr dashboardRow
	if in.Visibility != nil {
		// Visibility flips are owner-only, even for admins and
		// edit-shared members.
		dr, err = loadDashboardOwner(ctx, pool, wsID, role, dashboardID, actorID)
	} else {
		dr, err = loadDashboard(ctx, pool, wsID, role, dashboardID, actorID, true)
	}
	if err != nil {
		return nil, err
	}
	var sets []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, ErrNameRequired
		}
		sets = append(sets, "name = "+arg(name))
	}
	if in.Description != nil {
		sets = append(sets, "description = "+arg(*in.Description))
	}
	if in.Visibility != nil {
		if *in.Visibility != DashboardVisibilityPublic && *in.Visibility != DashboardVisibilityPrivate {
			return nil, ErrInvalidVisibility
		}
		sets = append(sets, "visibility = "+arg(*in.Visibility))
	}
	if len(sets) == 0 {
		return nil, ErrNothingToUpdate
	}
	sets = append(sets, "updated_at = now()")
	if _, err := pool.Exec(ctx,
		`UPDATE dashboards SET `+strings.Join(sets, ", ")+`
		  WHERE id = `+arg(dr.Dashboard.ID)+`::uuid`,
		args...); err != nil {
		return nil, err
	}
	// Re-read the row directly (no visibility gate: an admin may have
	// just flipped the dashboard to private, which the gated loader
	// would 404 for them).
	fresh, err := selectDashboardRow(ctx, pool, dashboardID, actorID)
	if err != nil {
		return nil, err
	}
	out := fresh.Dashboard
	return &out, nil
}

// listWidgets returns a dashboard's widgets, oldest first (stable
// canvas order; the frontend positions by grid_x/grid_y).
func listWidgets(ctx context.Context, pool *pgxpool.Pool, dashboardID string) ([]Widget, error) {
	rows, err := pool.Query(ctx,
		`SELECT `+widgetColumns+`
		   FROM dashboard_widgets w
		  WHERE w.dashboard_id = $1::uuid
		  ORDER BY w.created_at, w.id`,
		dashboardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Widget{}
	for rows.Next() {
		wd, err := scanWidget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, wd)
	}
	return out, rows.Err()
}

// CreateWidgetInput is the POST widget body.
type CreateWidgetInput struct {
	Title      string
	WidgetType string
	Config     json.RawMessage
	GridX      *int
	GridY      *int
	GridW      *int
	GridH      *int
}

// CreateWidget adds a widget to a dashboard under the dashboard's edit
// rule. Title and a valid widget_type are required; config is
// validated; grid units default (0,0,6,4) and are clamped.
func CreateWidget(ctx context.Context, pool *pgxpool.Pool, wsSlug, dashboardID, actorID string, in CreateWidgetInput) (*Widget, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return nil, ErrNameRequired
	}
	if !validWidgetType(in.WidgetType) {
		return nil, ErrInvalidWidgetType
	}
	if _, err := parseWidgetConfig(in.WidgetType, in.Config); err != nil {
		return nil, err
	}
	wsID, role, err := resolveDashboardWorkspace(ctx, pool, wsSlug, actorID)
	if err != nil {
		return nil, err
	}
	if _, err := loadDashboard(ctx, pool, wsID, role, dashboardID, actorID, true); err != nil {
		return nil, err
	}
	x, y, w, h := 0, 0, 6, 4
	if in.GridX != nil {
		x = *in.GridX
	}
	if in.GridY != nil {
		y = *in.GridY
	}
	if in.GridW != nil {
		w = *in.GridW
	}
	if in.GridH != nil {
		h = *in.GridH
	}
	x, y, w, h = clampGrid(x, y, w, h)
	var configJSON []byte
	if len(in.Config) == 0 {
		configJSON = []byte("{}")
	} else {
		configJSON = in.Config
	}
	var widgetID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO dashboard_widgets (dashboard_id, title, widget_type, config, grid_x, grid_y, grid_w, grid_h)
		 VALUES ($1::uuid, $2, $3, $4::jsonb, $5, $6, $7, $8)
		 RETURNING id::text`,
		dashboardID, title, in.WidgetType, string(configJSON), x, y, w, h).Scan(&widgetID); err != nil {
		return nil, err
	}
	wd, err := scanWidget(pool.QueryRow(ctx,
		`SELECT id::text, dashboard_id::text, title, widget_type, config,
		        grid_x, grid_y, grid_w, grid_h, created_at, updated_at
		   FROM dashboard_widgets WHERE id = $1::uuid`,
		widgetID))
	if err != nil {
		return nil, err
	}
	if _, err := pool.Exec(ctx, `UPDATE dashboards SET updated_at = now() WHERE id = $1::uuid`, dashboardID); err != nil {
		return nil, err
	}
	return &wd, nil
}

// UpdateWidgetInput is the PATCH widget body: every field optional.
type UpdateWidgetInput struct {
	Title      *string
	WidgetType *string
	Config     json.RawMessage // nil = unchanged; non-nil replaces wholesale
}

// UpdateWidget applies a partial widget update under the dashboard's
// edit rule. A changed widget_type revalidates against the (possibly
// replaced) config, so a stale group_by can never survive a type flip.
func UpdateWidget(ctx context.Context, pool *pgxpool.Pool, widgetID, actorID string, in UpdateWidgetInput) (*Widget, error) {
	_, wd, err := loadWidgetDashboard(ctx, pool, widgetID, actorID, true)
	if err != nil {
		return nil, err
	}
	effType := wd.WidgetType
	if in.WidgetType != nil {
		if !validWidgetType(*in.WidgetType) {
			return nil, ErrInvalidWidgetType
		}
		effType = *in.WidgetType
	}
	effConfig := wd.Config
	configTouched := in.Config != nil
	if configTouched {
		effConfig = in.Config
	}
	if in.WidgetType != nil || configTouched {
		if _, err := parseWidgetConfig(effType, effConfig); err != nil {
			return nil, err
		}
	}
	var sets []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if in.Title != nil {
		title := strings.TrimSpace(*in.Title)
		if title == "" {
			return nil, ErrNameRequired
		}
		sets = append(sets, "title = "+arg(title))
	}
	if in.WidgetType != nil {
		sets = append(sets, "widget_type = "+arg(effType))
	}
	if configTouched {
		if len(effConfig) == 0 {
			effConfig = json.RawMessage("{}")
		}
		sets = append(sets, "config = "+arg(string(effConfig))+"::jsonb")
	}
	if len(sets) == 0 {
		return nil, ErrNothingToUpdate
	}
	sets = append(sets, "updated_at = now()")
	updated, err := scanWidget(pool.QueryRow(ctx,
		`UPDATE dashboard_widgets SET `+strings.Join(sets, ", ")+`
		  WHERE id = `+arg(widgetID)+`::uuid
		 RETURNING id::text, dashboard_id::text, title, widget_type, config,
		           grid_x, grid_y, grid_w, grid_h, created_at, updated_at`,
		args...))
	if err != nil {
		return nil, err
	}
	if _, err := pool.Exec(ctx, `UPDATE dashboards SET updated_at = now() WHERE id = $1::uuid`, wd.DashboardID); err != nil {
		return nil, err
	}
	return &updated, nil
}

// UpdateWidgetLayoutInput is the PATCH …/layout body: grid fields only.
type UpdateWidgetLayoutInput struct {
	GridX *int
	GridY *int
	GridW *int
	GridH *int
}

// UpdateWidgetLayout commits a drag/resize without resending config.
// Unknown keys are rejected by the API layer; values are clamped.
func UpdateWidgetLayout(ctx context.Context, pool *pgxpool.Pool, widgetID, actorID string, in UpdateWidgetLayoutInput) (*Widget, error) {
	_, wd, err := loadWidgetDashboard(ctx, pool, widgetID, actorID, true)
	if err != nil {
		return nil, err
	}
	if in.GridX == nil && in.GridY == nil && in.GridW == nil && in.GridH == nil {
		return nil, ErrNothingToUpdate
	}
	x, y, w, h := wd.GridX, wd.GridY, wd.GridW, wd.GridH
	if in.GridX != nil {
		x = *in.GridX
	}
	if in.GridY != nil {
		y = *in.GridY
	}
	if in.GridW != nil {
		w = *in.GridW
	}
	if in.GridH != nil {
		h = *in.GridH
	}
	x, y, w, h = clampGrid(x, y, w, h)
	updated, err := scanWidget(pool.QueryRow(ctx,
		`UPDATE dashboard_widgets
		    SET grid_x = $1, grid_y = $2, grid_w = $3, grid_h = $4, updated_at = now()
		  WHERE id = $5::uuid
		 RETURNING id::text, dashboard_id::text, title, widget_type, config,
		           grid_x, grid_y, grid_w, grid_h, created_at, updated_at`,
		x, y, w, h, widgetID))
	if err != nil {
		return nil, err
	}
	if _, err := pool.Exec(ctx, `UPDATE dashboards SET updated_at = now() WHERE id = $1::uuid`, wd.DashboardID); err != nil {
		return nil, err
	}
	return &updated, nil
}

// DeleteWidget removes a widget under the dashboard's edit rule.
func DeleteWidget(ctx context.Context, pool *pgxpool.Pool, widgetID, actorID string) error {
	_, wd, err := loadWidgetDashboard(ctx, pool, widgetID, actorID, true)
	if err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `DELETE FROM dashboard_widgets WHERE id = $1::uuid`, widgetID); err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `UPDATE dashboards SET updated_at = now() WHERE id = $1::uuid`, wd.DashboardID)
	return err
}

// DeleteDashboard removes a dashboard (widgets and shares cascade).
// Owner-only: admins and edit-shared members get ErrForbidden.
func DeleteDashboard(ctx context.Context, pool *pgxpool.Pool, wsSlug, dashboardID, actorID string) error {
	wsID, role, err := resolveDashboardWorkspace(ctx, pool, wsSlug, actorID)
	if err != nil {
		return err
	}
	if _, err := loadDashboardOwner(ctx, pool, wsID, role, dashboardID, actorID); err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `DELETE FROM dashboards WHERE id = $1::uuid`, dashboardID)
	return err
}

// widgetProjectScope resolves the widget's project scope to the IDs of
// live (non-archived) projects of the workspace: the widget's
// project_ids intersected with the workspace when set, every
// non-archived workspace project when empty.
func widgetProjectScope(ctx context.Context, pool *pgxpool.Pool, wsID string, projectIDs []string) ([]string, error) {
	rows, err := pool.Query(ctx,
		`SELECT p.id::text FROM projects p
		  WHERE p.workspace_id = $1::uuid AND p.archived_at IS NULL`,
		wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	live := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		live[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(projectIDs) == 0 {
		out := make([]string, 0, len(live))
		for id := range live {
			out = append(out, id)
		}
		return out, nil
	}
	out := make([]string, 0, len(projectIDs))
	for _, id := range projectIDs {
		if live[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

// widgetIssueFilter builds the WHERE fragment (ANDed filters) shared by
// every widget-data query: live issues in scope, narrowed by the
// widget's filters.
func widgetIssueFilter(cfg *widgetConfig, projectIDs []string, args *[]any) string {
	arg := func(v any) string {
		*args = append(*args, v)
		return fmt.Sprintf("$%d", len(*args))
	}
	var conds []string
	conds = append(conds, "i.project_id = ANY("+arg(projectIDs)+"::uuid[])")
	conds = append(conds, "i.deleted_at IS NULL", "i.archived_at IS NULL")
	if len(cfg.states) > 0 {
		conds = append(conds, "i.state_id = ANY("+arg(cfg.states)+"::uuid[])")
	}
	if len(cfg.priorities) > 0 {
		conds = append(conds, "i.priority = ANY("+arg(cfg.priorities)+"::int[])")
	}
	if len(cfg.assignees) > 0 {
		conds = append(conds, `EXISTS (SELECT 1 FROM issue_assignees ia
			WHERE ia.issue_id = i.id AND ia.user_id = ANY(`+arg(cfg.assignees)+`::uuid[]))`)
	}
	if len(cfg.labels) > 0 {
		conds = append(conds, `EXISTS (SELECT 1 FROM issue_labels il
			WHERE il.issue_id = i.id AND il.label_id = ANY(`+arg(cfg.labels)+`::uuid[]))`)
	}
	return strings.Join(conds, " AND ")
}

// widgetCompute is the resolved context shared by the live data and
// CSV export paths: the dashboard row, the widget, its validated
// config, and its project scope.
type widgetCompute struct {
	dr         dashboardRow
	widget     Widget
	cfg        *widgetConfig
	projectIDs []string
}

// resolveWidgetCompute enforces the dashboard's read rule (owner|edit|
// view — a private dashboard of another creator without a share reads
// as a missing widget), validates the stored config, and resolves the
// project scope. Widget routes are top-level (/api/v1/widgets/…), so
// tenancy comes from the widget→dashboard→workspace join.
func resolveWidgetCompute(ctx context.Context, pool *pgxpool.Pool, widgetID, actorID string) (*widgetCompute, error) {
	dr, wd, err := loadWidgetDashboard(ctx, pool, widgetID, actorID, false)
	if err != nil {
		return nil, err
	}
	cfg, err := parseWidgetConfig(wd.WidgetType, wd.Config)
	if err != nil {
		// Config was valid at write time; a parse failure now means
		// data drift — surface it rather than silently returning
		// wrong numbers.
		return nil, err
	}
	projectIDs, err := widgetProjectScope(ctx, pool, dr.Dashboard.WorkspaceID, cfg.projectIDs)
	if err != nil {
		return nil, err
	}
	return &widgetCompute{dr: dr, widget: wd, cfg: cfg, projectIDs: projectIDs}, nil
}

// GetWidgetData recomputes a widget's data live from the issues table.
// Chart widgets return {groups:[{key,label,value}]}; number widgets
// return {value}; the paginated types (smart_counter, smart_gauge,
// table_2d) return {groups, page, max_rows, total_groups} with groups
// holding only the requested page (page >= 1, default 1 — anything else
// is ErrInvalidWidgetPage); intake_breakdown widgets return {intake:{…}}.
// Viewer role: any member (15)+ may read; a private dashboard of
// another creator without a share reads as a missing widget.
func GetWidgetData(ctx context.Context, pool *pgxpool.Pool, widgetID, actorID string, page int) (*WidgetData, error) {
	if page < 1 {
		return nil, ErrInvalidWidgetPage
	}
	wc, err := resolveWidgetCompute(ctx, pool, widgetID, actorID)
	if err != nil {
		return nil, err
	}
	return computeWidgetData(ctx, pool, wc, page, true)
}

// computeWidgetData runs the widget's live recompute. When paginate is
// true the paginated types return only the requested page; when false
// they return every group (the CSV export path — full dataset).
func computeWidgetData(ctx context.Context, pool *pgxpool.Pool, wc *widgetCompute, page int, paginate bool) (*WidgetData, error) {
	cfg := wc.cfg
	var args []any
	where := widgetIssueFilter(cfg, wc.projectIDs, &args)

	switch wc.widget.WidgetType {
	case WidgetTypeNumber:
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM issues i WHERE `+where, args...).Scan(&n); err != nil {
			return nil, err
		}
		return &WidgetData{Value: &n}, nil
	case WidgetTypeIntake:
		bd, err := computeIntakeBreakdown(ctx, pool, wc.dr.Dashboard.WorkspaceID)
		if err != nil {
			return nil, err
		}
		return &WidgetData{Intake: bd}, nil
	}

	groups, err := computeGrouped(ctx, pool, cfg, where, args)
	if err != nil {
		return nil, err
	}
	out := &WidgetData{}
	if wc.widget.WidgetType == WidgetTypeSmartCounter ||
		wc.widget.WidgetType == WidgetTypeSmartGauge ||
		wc.widget.WidgetType == WidgetTypeTable2D {
		total := len(groups)
		maxRows := cfg.maxRows
		if maxRows < 1 {
			maxRows = 10
		}
		if paginate {
			start := (page - 1) * maxRows
			if start < total {
				end := start + maxRows
				if end > total {
					end = total
				}
				groups = groups[start:end]
			} else {
				groups = []WidgetDataGroup{}
			}
		}
		out.Groups = groups
		out.Page = &page
		out.MaxRows = &maxRows
		out.TotalGroups = &total
		return out, nil
	}
	out.Groups = groups
	return out, nil
}

// metricAggregates returns the (total, completed) aggregate expressions
// for a grouped widget query. Every grouped query joins states AS s
// (for the completed share) and estimate_points AS ep (LEFT — issues
// without an estimate contribute 0 to point sums).
func metricAggregates(metric string) (total, completed string) {
	if metric == WidgetMetricEstimatePoints {
		return `COALESCE(SUM(ep.value), 0)`,
			`COALESCE(SUM(CASE WHEN s."group" = 'completed' THEN ep.value ELSE 0 END), 0)`
	}
	return `COUNT(*)`, `COUNT(*) FILTER (WHERE s."group" = 'completed')`
}

// groupDim describes one grouping dimension: the SELECT/GROUP BY
// expressions for the key, the extra joins it needs, its ordering, and
// whether it needs the synthetic bucket query (unassigned / no label).
type groupDim struct {
	keyExpr string
	joins   string
	groupBy string
	orderBy string
	bucket  string // "", "unassigned", or "unlabeled"
}

// groupDimFor resolves the SQL fragments for a group_by dimension. The
// states join (AS s) is always included: the gauge completion share
// needs it, and it is harmless for the other types.
func groupDimFor(dim string) (groupDim, error) {
	states := `JOIN states s ON s.id = i.state_id`
	ep := `LEFT JOIN estimate_points ep ON ep.id = i.estimate_point_id`
	switch dim {
	case WidgetGroupByState:
		return groupDim{
			keyExpr: `s.name`,
			joins:   states + "\n\t\t\t   " + ep,
			groupBy: `s.name`,
			orderBy: `MIN(s.sequence), s.name`,
		}, nil
	case WidgetGroupByPriority:
		return groupDim{
			keyExpr: `i.priority::text`,
			joins:   states + "\n\t\t\t   " + ep,
			groupBy: `i.priority`,
			orderBy: `i.priority`,
		}, nil
	case WidgetGroupByAssignee:
		return groupDim{
			keyExpr: `COALESCE(NULLIF(u.name, ''), u.email)`,
			joins: states + `
			   JOIN issue_assignees ia ON ia.issue_id = i.id
			   JOIN users u ON u.id = ia.user_id
			   ` + ep,
			groupBy: `COALESCE(NULLIF(u.name, ''), u.email)`,
			orderBy: `total DESC, key`,
			bucket:  "unassigned",
		}, nil
	case WidgetGroupByLabel:
		return groupDim{
			keyExpr: `l.name`,
			joins: states + `
			   JOIN issue_labels il ON il.issue_id = i.id
			   JOIN labels l ON l.id = il.label_id
			   ` + ep,
			groupBy: `l.name`,
			orderBy: `total DESC, key`,
			bucket:  "unlabeled",
		}, nil
	case WidgetGroupByStateGroup:
		return groupDim{
			keyExpr: `s."group"`,
			joins:   states + "\n\t\t\t   " + ep,
			groupBy: `s."group"`,
			orderBy: `CASE s."group" WHEN 'triage' THEN 0 WHEN 'backlog' THEN 1 WHEN 'unstarted' THEN 2 WHEN 'started' THEN 3 WHEN 'completed' THEN 4 WHEN 'cancelled' THEN 5 ELSE 6 END`,
		}, nil
	case WidgetGroupByProject:
		return groupDim{
			keyExpr: `p.name`,
			joins: states + `
			   JOIN projects p ON p.id = i.project_id
			   ` + ep,
			groupBy: `p.name`,
			orderBy: `total DESC, key`,
		}, nil
	case WidgetGroupByCreatedBy:
		return groupDim{
			keyExpr: `COALESCE(NULLIF(u.name, ''), u.email)`,
			joins: states + `
			   JOIN users u ON u.id = i.created_by
			   ` + ep,
			groupBy: `COALESCE(NULLIF(u.name, ''), u.email)`,
			orderBy: `total DESC, key`,
		}, nil
	}
	return groupDim{}, ErrInvalidWidgetConfig
}

// bucketWhere returns the extra WHERE fragment for a synthetic bucket.
func bucketWhere(bucket string) string {
	if bucket == "unassigned" {
		return `AND NOT EXISTS (SELECT 1 FROM issue_assignees ia WHERE ia.issue_id = i.id)`
	}
	return `AND NOT EXISTS (SELECT 1 FROM issue_labels il WHERE il.issue_id = i.id)`
}

// computeGrouped runs the per-group aggregation for the widget's
// group_by dimension and metric, returning every group in display
// order (pagination slices later). The value of a group is the issue
// count, or the summed estimate points when metric=estimate_points.
// For smart_gauge widgets each group also carries its completed share
// (issues in a 'completed'-group state) and 100*completed/value.
func computeGrouped(ctx context.Context, pool *pgxpool.Pool, cfg *widgetConfig, where string, args []any) ([]WidgetDataGroup, error) {
	dim, err := groupDimFor(cfg.groupBy)
	if err != nil {
		return nil, err
	}
	totalAgg, completedAgg := metricAggregates(cfg.metric)
	groups := []WidgetDataGroup{}
	rows, err := pool.Query(ctx,
		`SELECT `+dim.keyExpr+` AS key, `+totalAgg+` AS total, `+completedAgg+` AS completed
		   FROM issues i
		   `+dim.joins+`
		  WHERE `+where+`
		  GROUP BY `+dim.groupBy+`
		  ORDER BY `+dim.orderBy,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var total, completed int
		if err := rows.Scan(&key, &total, &completed); err != nil {
			return nil, err
		}
		groups = append(groups, labelGroup(cfg.groupBy, key, total, completed))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if dim.bucket != "" {
		var key, label string
		var total, completed, n int
		if dim.bucket == "unassigned" {
			key, label = "unassigned", "Unassigned"
		} else {
			key, label = "none", "No label"
		}
		if err := pool.QueryRow(ctx,
			`SELECT COUNT(*), `+totalAgg+`, `+completedAgg+`
			   FROM issues i
			   JOIN states s ON s.id = i.state_id
			   LEFT JOIN estimate_points ep ON ep.id = i.estimate_point_id
			  WHERE `+where+`
			    `+bucketWhere(dim.bucket),
			args...).Scan(&n, &total, &completed); err != nil {
			return nil, err
		}
		if n > 0 {
			groups = append(groups, WidgetDataGroup{
				Key: key, Label: label, Value: total,
				Completed: &completed, Percentage: gaugePct(completed, total),
			})
		}
	}
	return groups, nil
}

// gaugePct returns 100*completed/total, or 0 when total is 0 (nothing
// counted or nothing estimated — Plane parity).
func gaugePct(completed, total int) *float64 {
	var pct float64
	if total > 0 {
		pct = 100.0 * float64(completed) / float64(total)
	}
	return &pct
}

// labelGroup maps a raw group key to the human label the frontend
// renders, attaching the gauge completion fields.
func labelGroup(dim, key string, total, completed int) WidgetDataGroup {
	g := WidgetDataGroup{Key: key, Label: key, Value: total,
		Completed: &completed, Percentage: gaugePct(completed, total)}
	switch dim {
	case WidgetGroupByPriority:
		label := key
		if n, err := strconv.Atoi(key); err == nil && n >= 0 && n < len(priorityLabels) {
			label = priorityLabels[n]
		}
		g.Key, g.Label = label, label
	case WidgetGroupByStateGroup:
		if l, ok := stateGroupLabels[key]; ok {
			g.Label = l
		}
	}
	return g
}

// computeIntakeBreakdown computes the six headline numbers of the
// intake_breakdown widget over the dashboard's project scope (every
// non-archived project of the workspace). Widget-level filters and
// project_ids are deliberately ignored (Plane parity). Decided rows
// are the terminal triage states (rejected/accepted/duplicate); the
// average time to decide is the mean of updated_at − created_at over
// those rows, in seconds (0 when nothing is decided yet).
func computeIntakeBreakdown(ctx context.Context, pool *pgxpool.Pool, wsID string) (*IntakeBreakdown, error) {
	var b IntakeBreakdown
	var avg *float64
	err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FILTER (WHERE ii.source = 'in_app'),
		        COUNT(*) FILTER (WHERE ii.source = 'form'),
		        COUNT(*) FILTER (WHERE ii.status = $2),
		        COUNT(*) FILTER (WHERE ii.status = $3),
		        AVG(EXTRACT(EPOCH FROM (ii.updated_at - ii.created_at))) FILTER (WHERE ii.status IN ($2, $3, $4))
		   FROM intake_issues ii
		   JOIN intake it ON it.id = ii.intake_id
		   JOIN projects p ON p.id = it.project_id
		  WHERE p.workspace_id = $1::uuid AND p.archived_at IS NULL`,
		wsID, IntakeAccepted, IntakeRejected, IntakeDuplicate).
		Scan(&b.InAppVolume, &b.FormVolume, &b.TotalAccepted, &b.TotalRejected, &avg)
	if err != nil {
		return nil, err
	}
	// email_volume is ALWAYS 0: glance has no email ingestion.
	b.EmailVolume = 0
	if avg != nil {
		b.AvgDecideSeconds = *avg
	}
	return &b, nil
}

// DashboardShare is one member's access grant on a dashboard: who can
// see it and at which level. Only the dashboard owner manages shares;
// shared members cannot re-share.
type DashboardShare struct {
	MemberID   string    `json:"member_id"`
	MemberName string    `json:"member_name"`
	Access     string    `json:"access"`
	CreatedAt  time.Time `json:"created_at"`
}

// scanDashboardShare scans a share row (dashboardShareColumns order).
const dashboardShareColumns = `s.member_id::text, COALESCE(NULLIF(u.name, ''), u.email), s.access, s.created_at`

func scanDashboardShare(row pgx.Row) (DashboardShare, error) {
	var sh DashboardShare
	err := row.Scan(&sh.MemberID, &sh.MemberName, &sh.Access, &sh.CreatedAt)
	return sh, err
}

// GetDashboardShares returns the dashboard's share rows, oldest first.
// Owner-only: everyone else gets ErrForbidden; an invisible dashboard
// is ErrNotFound.
func GetDashboardShares(ctx context.Context, pool *pgxpool.Pool, wsSlug, dashboardID, actorID string) ([]DashboardShare, error) {
	wsID, role, err := resolveDashboardWorkspace(ctx, pool, wsSlug, actorID)
	if err != nil {
		return nil, err
	}
	if _, err := loadDashboardOwner(ctx, pool, wsID, role, dashboardID, actorID); err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT `+dashboardShareColumns+`
		   FROM dashboard_shares s
		   JOIN users u ON u.id = s.member_id
		  WHERE s.dashboard_id = $1::uuid
		  ORDER BY s.created_at, s.member_id`,
		dashboardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DashboardShare{}
	for rows.Next() {
		sh, err := scanDashboardShare(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

// ShareInput is one entry of the PUT shares body.
type ShareInput struct {
	MemberID string
	Access   string
}

// validateShareEntries checks every entry of a PUT shares body: UUID
// shape, view|edit access, no duplicates, not the dashboard creator,
// and an ACTIVE workspace member (role >= member — guests are
// rejected; non-members are rejected).
func validateShareEntries(ctx context.Context, pool *pgxpool.Pool, wsID, creatorID string, in []ShareInput) error {
	seen := map[string]bool{}
	for _, sh := range in {
		if !isUUIDFormat(sh.MemberID) {
			return fmt.Errorf("%w: member_id %q is not a UUID", ErrInvalidDashboardShare, sh.MemberID)
		}
		if sh.Access != DashboardShareView && sh.Access != DashboardShareEdit {
			return fmt.Errorf("%w: access must be one of view|edit", ErrInvalidDashboardShare)
		}
		if seen[sh.MemberID] {
			return fmt.Errorf("%w: duplicate member_id %q", ErrInvalidDashboardShare, sh.MemberID)
		}
		seen[sh.MemberID] = true
		if sh.MemberID == creatorID {
			return fmt.Errorf("%w: the dashboard creator already owns the dashboard", ErrInvalidDashboardShare)
		}
		var role int
		err := pool.QueryRow(ctx,
			`SELECT m.role FROM workspace_members m
			  WHERE m.workspace_id = $1::uuid AND m.user_id = $2::uuid`,
			wsID, sh.MemberID).Scan(&role)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: member %q is not a workspace member", ErrInvalidDashboardShare, sh.MemberID)
			}
			return err
		}
		if role < RoleMember {
			return fmt.Errorf("%w: guests cannot be added to dashboard shares", ErrInvalidDashboardShare)
		}
	}
	return nil
}

// ReplaceDashboardShares replaces the dashboard's whole share list in
// one transaction (Plane's one-batch model): entries present in the
// body are added/updated, entries absent are removed. An empty list
// clears every share. Owner-only.
func ReplaceDashboardShares(ctx context.Context, pool *pgxpool.Pool, wsSlug, dashboardID, actorID string, in []ShareInput) ([]DashboardShare, error) {
	wsID, role, err := resolveDashboardWorkspace(ctx, pool, wsSlug, actorID)
	if err != nil {
		return nil, err
	}
	dr, err := loadDashboardOwner(ctx, pool, wsID, role, dashboardID, actorID)
	if err != nil {
		return nil, err
	}
	if err := validateShareEntries(ctx, pool, wsID, dr.Dashboard.CreatedBy, in); err != nil {
		return nil, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM dashboard_shares WHERE dashboard_id = $1::uuid`, dashboardID); err != nil {
		return nil, err
	}
	for _, sh := range in {
		if _, err := tx.Exec(ctx,
			`INSERT INTO dashboard_shares (dashboard_id, member_id, access)
			 VALUES ($1::uuid, $2::uuid, $3)`,
			dashboardID, sh.MemberID, sh.Access); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return GetDashboardShares(ctx, pool, wsSlug, dashboardID, actorID)
}

// csvExportCap is the maximum data rows a widget CSV carries; past it
// the payload is truncated and the response carries
// X-Result-Truncated: true.
const csvExportCap = 10000

// slugify renders a dashboard/widget name filename-safe: lowercase,
// runs of non-alphanumerics collapse to one dash, no leading/trailing
// dashes. Empty input becomes "untitled".
func slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	dash := true // true = last char was a dash (suppress leading)
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "untitled"
	}
	return out
}

// ExportWidgetCSV renders the widget's FULL live dataset as CSV — the
// same recompute as GetWidgetData, but unpaginated (every group, not
// just the current page), capped at csvExportCap rows. Columns per
// type: key,label,value (charts, smart_counter, table_2d);
// key,label,completed,total,percentage (smart_gauge);
// metric,value (intake_breakdown); value (number). Proper CSV quoting
// via encoding/csv. Read authZ = the widget read rule (a shared-view
// member may export). The filename is
// "<dashboard-slug>-<widget-slug>.csv", sanitized.
func ExportWidgetCSV(ctx context.Context, pool *pgxpool.Pool, widgetID, actorID string) (filename string, data []byte, truncated bool, err error) {
	wc, err := resolveWidgetCompute(ctx, pool, widgetID, actorID)
	if err != nil {
		return "", nil, false, err
	}
	full, err := computeWidgetData(ctx, pool, wc, 1, false)
	if err != nil {
		return "", nil, false, err
	}
	var rows [][]string
	switch wc.widget.WidgetType {
	case WidgetTypeSmartGauge:
		rows = append(rows, []string{"key", "label", "completed", "total", "percentage"})
		for _, g := range full.Groups {
			completed, pct := 0, 0.0
			if g.Completed != nil {
				completed = *g.Completed
			}
			if g.Percentage != nil {
				pct = *g.Percentage
			}
			rows = append(rows, []string{g.Key, g.Label,
				strconv.Itoa(completed), strconv.Itoa(g.Value),
				strconv.FormatFloat(pct, 'f', 2, 64)})
		}
	case WidgetTypeIntake:
		rows = append(rows, []string{"metric", "value"})
		bd := full.Intake
		rows = append(rows,
			[]string{"in_app_volume", strconv.Itoa(bd.InAppVolume)},
			[]string{"email_volume", strconv.Itoa(bd.EmailVolume)},
			[]string{"form_volume", strconv.Itoa(bd.FormVolume)},
			[]string{"total_accepted", strconv.Itoa(bd.TotalAccepted)},
			[]string{"total_rejected", strconv.Itoa(bd.TotalRejected)},
			[]string{"avg_decide_seconds", strconv.FormatFloat(bd.AvgDecideSeconds, 'f', 2, 64)},
		)
	case WidgetTypeNumber:
		rows = append(rows, []string{"value"})
		v := 0
		if full.Value != nil {
			v = *full.Value
		}
		rows = append(rows, []string{strconv.Itoa(v)})
	default:
		rows = append(rows, []string{"key", "label", "value"})
		for _, g := range full.Groups {
			rows = append(rows, []string{g.Key, g.Label, strconv.Itoa(g.Value)})
		}
	}
	if len(rows)-1 > csvExportCap {
		rows = rows[:csvExportCap+1]
		truncated = true
	}
	var buf strings.Builder
	w := csv.NewWriter(&buf)
	for _, r := range rows {
		if err := w.Write(r); err != nil {
			return "", nil, false, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", nil, false, err
	}
	filename = slugify(wc.dr.Dashboard.Name) + "-" + slugify(wc.widget.Title) + ".csv"
	return filename, []byte(buf.String()), truncated, nil
}
