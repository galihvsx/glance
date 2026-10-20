package service

// Customizable dashboards, phase 1 (C81T0): workspace-scoped data
// canvases à la Plane Dashboards — a 12-column grid of widgets, each
// independently configured (chart type, grouping, filters, project
// scope) and recomputed live from work-item data.
//
// AuthZ (Plane's exact rule):
//   - Reads (list/get/widget data): workspace member (15)+; guests
//     (5) get ErrForbidden, bad slug / non-member ErrNotFound.
//   - Visibility: public dashboards are visible to all members;
//     a private dashboard is visible only to its creator — any other
//     caller gets ErrNotFound (never a 403 that would leak existence).
//   - Edit (PATCH/DELETE dashboard, all widget writes): the creator, or
//     a workspace admin (20) on PUBLIC dashboards only; on private
//     dashboards only the creator. Non-creator non-admin on a public
//     dashboard → ErrForbidden; on a private dashboard → ErrNotFound.
//   - Malformed UUIDs never reach the service: the API layer 400s them
//     first (requireUUIDParam); the service still guards with
//     isUUIDFormat where it parses stored/filter input.
//
// Config validation (honest 400s at write time):
//   - widget_type ∈ bar|line|area|donut|pie|number (also a DB CHECK).
//   - group_by ∈ state|priority|assignee|label — required for the five
//     chart types, optional and ignored for number.
//   - metric is reserved: when present it must be "count" (phase 1 has
//     no other metric).
//   - filters.{assignees,states,labels} are UUID arrays;
//     filters.priorities are ints 0..4; unknown keys are rejected.
//   - project_ids is a UUID array; unknown keys are rejected.
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
// name (COALESCE(NULLIF(name,''), email)), label name. Assignee
// grouping adds an "Unassigned" bucket and label grouping a "No label"
// bucket so the groups partition every counted issue.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	WidgetTypeBar    = "bar"
	WidgetTypeLine   = "line"
	WidgetTypeArea   = "area"
	WidgetTypeDonut  = "donut"
	WidgetTypePie    = "pie"
	WidgetTypeNumber = "number"
)

// Widget group-by dimensions.
const (
	WidgetGroupByState    = "state"
	WidgetGroupByPriority = "priority"
	WidgetGroupByAssignee = "assignee"
	WidgetGroupByLabel    = "label"
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
	// the six chart types. The handler maps it to 400 bad_request.
	ErrInvalidWidgetType = errors.New("service: invalid widget_type")
	// ErrInvalidWidgetConfig is returned when the widget config JSON
	// fails validation (bad group_by, non-"count" metric, malformed
	// filter/project_ids values, unknown keys). 400 bad_request.
	ErrInvalidWidgetConfig = errors.New("service: invalid widget config")
)

// priorityLabels mirrors web/src/lib/types.ts PRIORITY_LABELS — the
// human labels the frontend renders for priority buckets.
var priorityLabels = []string{"None", "Low", "Medium", "High", "Urgent"}

// Dashboard is a workspace-scoped widget canvas. Widgets ride along on
// the detail read only (the list read omits them); the frontend fetches
// per-widget data separately via GetWidgetData.
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
// assignee display name, label name) — same-named buckets across
// projects merge. "Unassigned" (assignee) and "No label" (label) are
// synthetic buckets so the groups partition every counted issue.
type WidgetDataGroup struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Value int    `json:"value"`
}

// WidgetData is the live recompute result: groups for the five chart
// types, a scalar value for number widgets.
type WidgetData struct {
	Groups []WidgetDataGroup `json:"groups,omitempty"`
	Value  *int              `json:"value,omitempty"`
}

// dashboardRow is the internal dashboard row plus the creator check.
type dashboardRow struct {
	Dashboard
	isCreator bool
	role      int
}

// validWidgetType reports whether t is one of the six chart types.
func validWidgetType(t string) bool {
	switch t {
	case WidgetTypeBar, WidgetTypeLine, WidgetTypeArea,
		WidgetTypeDonut, WidgetTypePie, WidgetTypeNumber:
		return true
	}
	return false
}

// validGroupBy reports whether g is a supported grouping dimension.
func validGroupBy(g string) bool {
	switch g {
	case WidgetGroupByState, WidgetGroupByPriority, WidgetGroupByAssignee, WidgetGroupByLabel:
		return true
	}
	return false
}

// widgetConfig is the validated, normalized form of a widget's config
// JSON. Filters are ANDed; an empty projectIDs means "every
// non-archived project of the workspace".
type widgetConfig struct {
	groupBy    string
	metric     string
	assignees  []string
	priorities []int
	states     []string
	labels     []string
	projectIDs []string
}

// parseWidgetConfig validates raw config JSON for the given widget type
// and returns its normalized form. Unknown keys, bad enums, malformed
// UUIDs, and out-of-range priorities are all ErrInvalidWidgetConfig.
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
		case "group_by", "metric", "filters", "project_ids":
		default:
			return nil, fmt.Errorf("%w: unknown config key %q", ErrInvalidWidgetConfig, k)
		}
	}
	cfg := &widgetConfig{metric: "count"}

	if rawGB, ok := top["group_by"]; ok {
		var gb string
		if err := json.Unmarshal(rawGB, &gb); err != nil || !validGroupBy(gb) {
			return nil, fmt.Errorf("%w: group_by must be one of state|priority|assignee|label", ErrInvalidWidgetConfig)
		}
		cfg.groupBy = gb
	}
	if rawM, ok := top["metric"]; ok {
		var m string
		if err := json.Unmarshal(rawM, &m); err != nil || m != "count" {
			return nil, fmt.Errorf("%w: metric is reserved: only \"count\" is supported in phase 1", ErrInvalidWidgetConfig)
		}
		cfg.metric = m
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

	if widgetType == WidgetTypeNumber {
		// group_by is meaningless for a scalar; ignore it rather than
		// rejecting a stale config when the type flips to number.
		cfg.groupBy = ""
	} else if cfg.groupBy == "" {
		return nil, fmt.Errorf("%w: group_by is required for %q widgets", ErrInvalidWidgetConfig, widgetType)
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

// loadDashboard fetches the dashboard and enforces the visibility + edit
// rules. edit=true additionally requires Plane's edit rule (creator, or
// admin on public dashboards). A private dashboard of another creator
// is ErrNotFound for everyone else — admins included.
func loadDashboard(ctx context.Context, pool *pgxpool.Pool, wsID string, role int, dashboardID, actorID string, edit bool) (dashboardRow, error) {
	dr, err := scanDashboard(pool.QueryRow(ctx,
		`SELECT `+dashboardColumns+`, d.created_by = $3::uuid
		   FROM dashboards d
		   JOIN users u ON u.id = d.created_by
		  WHERE d.id = $1::uuid AND d.workspace_id = $2::uuid`,
		dashboardID, wsID, actorID), true)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dashboardRow{}, ErrDashboardNotFound
		}
		return dashboardRow{}, err
	}
	dr.role = role
	if dr.Dashboard.Visibility == DashboardVisibilityPrivate && !dr.isCreator {
		return dashboardRow{}, ErrNotFound
	}
	if edit && !dr.isCreator && role < RoleAdmin {
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

// ListDashboards returns the workspace's dashboards visible to the
// caller: public ones plus the caller's own private ones, newest
// updated first. Member (15)+; guests ErrForbidden. Widgets are NOT
// included (the detail read carries them).
func ListDashboards(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID string) ([]Dashboard, error) {
	wsID, _, err := resolveDashboardWorkspace(ctx, pool, wsSlug, actorID)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT `+dashboardColumns+`
		   FROM dashboards d
		   JOIN users u ON u.id = d.created_by
		  WHERE d.workspace_id = $1::uuid
		    AND (d.visibility = 'public' OR d.created_by = $2::uuid)
		  ORDER BY d.updated_at DESC, d.id DESC`,
		wsID, actorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Dashboard{}
	for rows.Next() {
		dr, err := scanDashboard(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, dr.Dashboard)
	}
	return out, rows.Err()
}

// GetDashboard returns one dashboard with its widgets (config only —
// data is fetched per widget via GetWidgetData). Private dashboards of
// another creator are ErrNotFound.
func GetDashboard(ctx context.Context, pool *pgxpool.Pool, wsSlug, dashboardID, actorID string) (*Dashboard, error) {
	wsID, role, err := resolveDashboardWorkspace(ctx, pool, wsSlug, actorID)
	if err != nil {
		return nil, err
	}
	dr, err := loadDashboard(ctx, pool, wsID, role, dashboardID, actorID, false)
	if err != nil {
		return nil, err
	}
	widgets, err := listWidgets(ctx, pool, dr.Dashboard.ID)
	if err != nil {
		return nil, err
	}
	out := dr.Dashboard
	out.Widgets = widgets
	return &out, nil
}

// UpdateDashboardInput is the PATCH body: every field optional.
type UpdateDashboardInput struct {
	Name        *string
	Description *string
	Visibility  *string
}

// UpdateDashboard applies a partial update under Plane's edit rule:
// creator, or admin on public dashboards; private dashboards creator
// only (everyone else sees ErrNotFound).
func UpdateDashboard(ctx context.Context, pool *pgxpool.Pool, wsSlug, dashboardID, actorID string, in UpdateDashboardInput) (*Dashboard, error) {
	wsID, role, err := resolveDashboardWorkspace(ctx, pool, wsSlug, actorID)
	if err != nil {
		return nil, err
	}
	dr, err := loadDashboard(ctx, pool, wsID, role, dashboardID, actorID, true)
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

// DeleteDashboard removes a dashboard (widgets cascade) under Plane's
// edit rule.
func DeleteDashboard(ctx context.Context, pool *pgxpool.Pool, wsSlug, dashboardID, actorID string) error {
	wsID, role, err := resolveDashboardWorkspace(ctx, pool, wsSlug, actorID)
	if err != nil {
		return err
	}
	if _, err := loadDashboard(ctx, pool, wsID, role, dashboardID, actorID, true); err != nil {
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

// GetWidgetData recomputes a widget's data live from the issues table.
// Chart widgets return {groups:[{key,label,value}]}; number widgets
// return {value}. Viewer role: any member (15)+ may read; a private
// dashboard of another creator reads as a missing widget.
func GetWidgetData(ctx context.Context, pool *pgxpool.Pool, widgetID, actorID string) (*WidgetData, error) {
	// Widget routes are top-level (/api/v1/widgets/…): tenancy resolves
	// through the widget→dashboard→workspace join.
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
	var args []any
	where := widgetIssueFilter(cfg, projectIDs, &args)

	if wd.WidgetType == WidgetTypeNumber {
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM issues i WHERE `+where, args...).Scan(&n); err != nil {
			return nil, err
		}
		return &WidgetData{Value: &n}, nil
	}

	groups := []WidgetDataGroup{}
	switch cfg.groupBy {
	case WidgetGroupByState:
		// Keyed by state NAME (the human label the frontend renders):
		// same-named states across projects merge into one bucket.
		rows, err := pool.Query(ctx,
			`SELECT s.name, COUNT(*)
			   FROM issues i
			   JOIN states s ON s.id = i.state_id
			  WHERE `+where+`
			  GROUP BY s.name
			  ORDER BY MIN(s.sequence), s.name`,
			args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var g WidgetDataGroup
			if err := rows.Scan(&g.Label, &g.Value); err != nil {
				return nil, err
			}
			g.Key = g.Label
			groups = append(groups, g)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	case WidgetGroupByPriority:
		rows, err := pool.Query(ctx,
			`SELECT i.priority, COUNT(*)
			   FROM issues i
			  WHERE `+where+`
			  GROUP BY i.priority
			  ORDER BY i.priority`,
			args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var prio, n int
			if err := rows.Scan(&prio, &n); err != nil {
				return nil, err
			}
			label := fmt.Sprintf("P%d", prio)
			if prio >= 0 && prio < len(priorityLabels) {
				label = priorityLabels[prio]
			}
			groups = append(groups, WidgetDataGroup{
				Key:   label,
				Label: label,
				Value: n,
			})
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	case WidgetGroupByAssignee:
		// Keyed by display name (the human label): same-named members
		// merge into one bucket.
		rows, err := pool.Query(ctx,
			`SELECT COALESCE(NULLIF(u.name, ''), u.email), COUNT(*)
			   FROM issues i
			   JOIN issue_assignees ia ON ia.issue_id = i.id
			   JOIN users u ON u.id = ia.user_id
			  WHERE `+where+`
			  GROUP BY COALESCE(NULLIF(u.name, ''), u.email)
			  ORDER BY COUNT(*) DESC, COALESCE(NULLIF(u.name, ''), u.email)`,
			args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var g WidgetDataGroup
			if err := rows.Scan(&g.Label, &g.Value); err != nil {
				return nil, err
			}
			g.Key = g.Label
			groups = append(groups, g)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		var unassigned int
		if err := pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM issues i
			  WHERE `+where+`
			    AND NOT EXISTS (SELECT 1 FROM issue_assignees ia WHERE ia.issue_id = i.id)`,
			args...).Scan(&unassigned); err != nil {
			return nil, err
		}
		if unassigned > 0 {
			groups = append(groups, WidgetDataGroup{Key: "unassigned", Label: "Unassigned", Value: unassigned})
		}
	case WidgetGroupByLabel:
		// Keyed by label name (the human label): workspace labels are
		// UNIQUE(workspace_id, name), so the name is a stable key.
		rows, err := pool.Query(ctx,
			`SELECT l.name, COUNT(*)
			   FROM issues i
			   JOIN issue_labels il ON il.issue_id = i.id
			   JOIN labels l ON l.id = il.label_id
			  WHERE `+where+`
			  GROUP BY l.name
			  ORDER BY COUNT(*) DESC, l.name`,
			args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var g WidgetDataGroup
			if err := rows.Scan(&g.Label, &g.Value); err != nil {
				return nil, err
			}
			g.Key = g.Label
			groups = append(groups, g)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		var unlabeled int
		if err := pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM issues i
			  WHERE `+where+`
			    AND NOT EXISTS (SELECT 1 FROM issue_labels il WHERE il.issue_id = i.id)`,
			args...).Scan(&unlabeled); err != nil {
			return nil, err
		}
		if unlabeled > 0 {
			groups = append(groups, WidgetDataGroup{Key: "none", Label: "No label", Value: unlabeled})
		}
	default:
		return nil, ErrInvalidWidgetConfig
	}
	return &WidgetData{Groups: groups}, nil
}
