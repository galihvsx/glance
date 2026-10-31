package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// DashboardHandler serves the customizable-dashboards API (C81T0):
// workspace-scoped dashboard CRUD plus widget CRUD and the live
// per-widget data endpoint. Route groups mirror the nesting:
// /api/v1/workspaces/{slug}/dashboards… for dashboard-scoped routes and
// top-level /api/v1/widgets/… for widget identity routes (a widget UUID
// is globally unique; tenancy resolves through the
// widget→dashboard→workspace join).
type DashboardHandler struct {
	Pool *pgxpool.Pool
}

// RegisterDashboardRoutes mounts the dashboards endpoints. Call this
// before the SPA catch-all so API routes are never shadowed. Keep in
// lockstep with registerAllAPIRoutes in internal/api/openapi_test.go and
// the registration sequence in cmd/glance/main.go.
func RegisterDashboardRoutes(e *echo.Echo, h *DashboardHandler) {
	g := e.Group("/api/v1/workspaces/:slug/dashboards", RequireAuth(h.Pool))
	g.GET("", h.listDashboards)
	g.POST("", h.createDashboard)
	g.GET("/:id", h.getDashboard)
	g.PATCH("/:id", h.updateDashboard)
	g.DELETE("/:id", h.deleteDashboard)
	g.GET("/:id/widgets", h.listWidgets)
	g.POST("/:id/widgets", h.createWidget)
	// C83T0: Plane's one-batch share model — PUT replaces the whole
	// share list. Owner-only.
	g.GET("/:id/shares", h.getDashboardShares)
	g.PUT("/:id/shares", h.replaceDashboardShares)

	// C83T0: the static template catalog (a sibling of /dashboards, not
	// nested under it).
	t := e.Group("/api/v1/workspaces/:slug/dashboard-templates", RequireAuth(h.Pool))
	t.GET("", h.listDashboardTemplates)

	wg := e.Group("/api/v1/widgets", RequireAuth(h.Pool))
	wg.PATCH("/:widget_id", h.updateWidget)
	wg.DELETE("/:widget_id", h.deleteWidget)
	// C81T0: layout-only commit for drag/resize — the canvas commits
	// grid_x/grid_y/grid_w/grid_h without resending config.
	wg.PATCH("/:widget_id/layout", h.updateWidgetLayout)
	wg.GET("/:widget_id/data", h.getWidgetData)
	// C83T0: per-widget CSV export of the full live dataset.
	wg.GET("/:widget_id/export.csv", h.exportWidgetCSV)
	// C84T0: drill-down into one chart segment — the items behind the
	// group (GET /api/v1/widgets/:id/drilldown?group=<group-key>).
	wg.GET("/:widget_id/drilldown", h.getWidgetDrilldown)
	// C86T0: intake trend series (GET /api/v1/widgets/:id/trend
	// ?granularity=day|week|month&buckets=1-52).
	wg.GET("/:widget_id/trend", h.getWidgetTrend)
}

// dashboardError maps dashboard/widget sentinel errors to HTTP statuses,
// delegating the shared sentinels (ErrNotFound, ErrForbidden,
// ErrNameRequired, ErrNothingToUpdate) to issueError. ErrInvalidWidgetConfig
// and ErrInvalidDashboardShare carry a wrapped detail ("service:
// invalid …: <detail>"); the prefix is stripped so the message stays
// client-shaped.
func dashboardError(c *echo.Context, err error) error {
	// C100T0: a PQL failure is a client error with message +
	// position, never a 500 — the same mapping the issue list uses.
	// It fires both at write time (a syntactically bad stored query,
	// wrapped in ErrInvalidWidgetConfig by parseWidgetConfig) and at
	// data time (a stored query whose names no longer resolve after a
	// rename), so it precedes the ErrInvalidWidgetConfig case.
	var pqlErr *service.PQLQueryError
	if errors.As(err, &pqlErr) {
		var details any
		if pqlErr.Pos > 0 {
			details = map[string]any{"position": pqlErr.Pos}
		}
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, pqlErr.Msg, details)
	}
	switch {
	case errors.Is(err, service.ErrDashboardNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "dashboard not found", nil)
	case errors.Is(err, service.ErrWidgetNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "widget not found", nil)
	case errors.Is(err, service.ErrInvalidVisibility):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, `invalid visibility: want "public" or "private"`, nil)
	case errors.Is(err, service.ErrInvalidWidgetType):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid widget_type: want one of bar|line|area|donut|pie|number|smart_counter|smart_gauge|table_2d|intake_breakdown|work_items_table|assigned_to_me|pql_table", nil)
	case errors.Is(err, service.ErrInvalidDrilldownGroup):
		msg := strings.TrimPrefix(err.Error(), "service: invalid drill-down group: ")
		msg = strings.TrimPrefix(msg, "service: invalid drill-down group")
		if msg == "" {
			msg = "invalid drill-down group"
		}
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, msg, nil)
	case errors.Is(err, service.ErrInvalidDrilldownMetric):
		msg := strings.TrimPrefix(err.Error(), "service: invalid drill-down metric: ")
		msg = strings.TrimPrefix(msg, "service: invalid drill-down metric")
		if msg == "" {
			msg = "invalid drill-down metric"
		}
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, msg, nil)
	case errors.Is(err, service.ErrInvalidDrilldownPerPage):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid per_page: want 1-100", nil)
	case errors.Is(err, service.ErrInvalidDrilldownBucket):
		msg := strings.TrimPrefix(err.Error(), "service: invalid drill-down bucket: ")
		msg = strings.TrimPrefix(msg, "service: invalid drill-down bucket")
		if msg == "" {
			msg = "invalid drill-down bucket"
		}
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, msg, nil)
	case errors.Is(err, service.ErrDrilldownUnavailable):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "drill-down is not available for this widget type", nil)
	case errors.Is(err, service.ErrInvalidTrendGranularity):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid granularity: want day|week|month", nil)
	case errors.Is(err, service.ErrInvalidTrendBuckets):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid buckets: want 1-52", nil)
	case errors.Is(err, service.ErrTrendUnavailable):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "trend is only available for intake_breakdown widgets", nil)
	case errors.Is(err, service.ErrInvalidWidgetConfig):
		msg := strings.TrimPrefix(err.Error(), "service: invalid widget config: ")
		msg = strings.TrimPrefix(msg, "service: invalid widget config")
		if msg == "" {
			msg = "invalid widget config"
		}
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, msg, nil)
	case errors.Is(err, service.ErrInvalidWidgetPage):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid page: want an integer >= 1", nil)
	case errors.Is(err, service.ErrInvalidDashboardShare):
		msg := strings.TrimPrefix(err.Error(), "service: invalid dashboard share: ")
		msg = strings.TrimPrefix(msg, "service: invalid dashboard share")
		if msg == "" {
			msg = "invalid dashboard share"
		}
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, msg, nil)
	case errors.Is(err, service.ErrUnknownDashboardTemplate):
		msg := strings.TrimPrefix(err.Error(), "service: unknown dashboard template: ")
		if msg == "" || msg == err.Error() {
			msg = "unknown dashboard template"
		} else {
			msg = "unknown dashboard template: " + msg
		}
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, msg, nil)
	default:
		return issueError(c, err)
	}
}

type createDashboardBody struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Visibility  string  `json:"visibility"`
	// Template, when non-nil, selects create-from-template mode: the
	// dashboard and all template widgets are created in one
	// transaction (visibility public, name defaulting to the template
	// name). It is mutually exclusive with description/visibility —
	// combining them is 400.
	Template *string `json:"template"`
}

type updateDashboardBody struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Visibility  *string `json:"visibility"`
}

// listDashboards implements GET /api/v1/workspaces/{slug}/dashboards:
// public dashboards, the caller's own private ones, and private ones
// shared with the caller (view or edit), newest updated first. Each
// row carries shared_with_me, my_access (owner|view|edit), and the
// shared indicator (owned AND shared out — feeds the Shared tab).
// Member (15)+; guests 403; bad slug / non-member 404.
func (h *DashboardHandler) listDashboards(c *echo.Context) error {
	dashboards, err := service.ListDashboards(c.Request().Context(), h.Pool, c.Param("slug"), CurrentUser(c).ID)
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"dashboards": dashboards})
}

// createDashboard implements POST /api/v1/workspaces/{slug}/dashboards.
// Two modes: the hand-rolled body ({name, description?, visibility?},
// visibility defaulting to public) or create-from-template
// ({"template": "<id>", "name"?} — name defaults to the template
// name). The two shapes are mutually exclusive: template combined
// with description or visibility is 400. Unknown template ids are 400.
func (h *DashboardHandler) createDashboard(c *echo.Context) error {
	var body createDashboardBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	if body.Template != nil {
		if body.Description != nil || body.Visibility != "" {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "template cannot be combined with description or visibility", nil)
		}
		d, err := service.CreateDashboardFromTemplate(c.Request().Context(), h.Pool,
			c.Param("slug"), CurrentUser(c).ID, *body.Template, body.Name)
		if err != nil {
			return dashboardError(c, err)
		}
		return c.JSON(http.StatusCreated, d)
	}
	d, err := service.CreateDashboard(c.Request().Context(), h.Pool, c.Param("slug"), CurrentUser(c).ID,
		body.Name, body.Description, body.Visibility)
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusCreated, d)
}

// getDashboard implements GET /api/v1/workspaces/{slug}/dashboards/{id}:
// the dashboard with its widgets (config only — data is fetched per
// widget). A private dashboard of another creator is 404, never 403.
func (h *DashboardHandler) getDashboard(c *echo.Context) error {
	id, ok := requireUUIDParam(c, "id", "dashboard id")
	if !ok {
		return nil
	}
	d, err := service.GetDashboard(c.Request().Context(), h.Pool, c.Param("slug"), id, CurrentUser(c).ID)
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusOK, d)
}

// updateDashboard implements PATCH
// /api/v1/workspaces/{slug}/dashboards/{id}. Name/description need the
// edit level (owner, edit-share, or admin on public dashboards); a
// visibility change is owner-only (403 for everyone else, even admins
// and edit-shared members). A private dashboard invisible to the
// caller is 404.
func (h *DashboardHandler) updateDashboard(c *echo.Context) error {
	id, ok := requireUUIDParam(c, "id", "dashboard id")
	if !ok {
		return nil
	}
	var body updateDashboardBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	d, err := service.UpdateDashboard(c.Request().Context(), h.Pool, c.Param("slug"), id, CurrentUser(c).ID,
		service.UpdateDashboardInput{Name: body.Name, Description: body.Description, Visibility: body.Visibility})
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusOK, d)
}

// deleteDashboard implements DELETE
// /api/v1/workspaces/{slug}/dashboards/{id} (widgets cascade).
func (h *DashboardHandler) deleteDashboard(c *echo.Context) error {
	id, ok := requireUUIDParam(c, "id", "dashboard id")
	if !ok {
		return nil
	}
	if err := service.DeleteDashboard(c.Request().Context(), h.Pool, c.Param("slug"), id, CurrentUser(c).ID); err != nil {
		return dashboardError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// listWidgets implements GET
// /api/v1/workspaces/{slug}/dashboards/{id}/widgets.
func (h *DashboardHandler) listWidgets(c *echo.Context) error {
	id, ok := requireUUIDParam(c, "id", "dashboard id")
	if !ok {
		return nil
	}
	d, err := service.GetDashboard(c.Request().Context(), h.Pool, c.Param("slug"), id, CurrentUser(c).ID)
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"widgets": d.Widgets})
}

type createWidgetBody struct {
	Title      string          `json:"title"`
	WidgetType string          `json:"widget_type"`
	Config     json.RawMessage `json:"config"`
	GridX      *int            `json:"grid_x"`
	GridY      *int            `json:"grid_y"`
	GridW      *int            `json:"grid_w"`
	GridH      *int            `json:"grid_h"`
}

// createWidget implements POST
// /api/v1/workspaces/{slug}/dashboards/{id}/widgets. Grid units default
// to (0,0,6,4) and are clamped to the 12-column canvas.
func (h *DashboardHandler) createWidget(c *echo.Context) error {
	id, ok := requireUUIDParam(c, "id", "dashboard id")
	if !ok {
		return nil
	}
	var body createWidgetBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	w, err := service.CreateWidget(c.Request().Context(), h.Pool, c.Param("slug"), id, CurrentUser(c).ID,
		service.CreateWidgetInput{
			Title:      body.Title,
			WidgetType: body.WidgetType,
			Config:     body.Config,
			GridX:      body.GridX,
			GridY:      body.GridY,
			GridW:      body.GridW,
			GridH:      body.GridH,
		})
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusCreated, w)
}

type updateWidgetBody struct {
	Title      *string          `json:"title"`
	WidgetType *string          `json:"widget_type"`
	Config     *json.RawMessage `json:"config"`
}

// updateWidget implements PATCH /api/v1/widgets/{widget_id}. config,
// when present, replaces wholesale and revalidates against the
// (possibly new) widget type.
func (h *DashboardHandler) updateWidget(c *echo.Context) error {
	widgetID, ok := requireUUIDParam(c, "widget_id", "widget id")
	if !ok {
		return nil
	}
	var body updateWidgetBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	in := service.UpdateWidgetInput{Title: body.Title, WidgetType: body.WidgetType}
	if body.Config != nil {
		in.Config = *body.Config
	}
	w, err := service.UpdateWidget(c.Request().Context(), h.Pool, widgetID, CurrentUser(c).ID, in)
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusOK, w)
}

// updateWidgetLayout implements PATCH /api/v1/widgets/{widget_id}/layout:
// grid_x/grid_y/grid_w/grid_h ONLY — the drag/resize commit path. AuthZ
// runs before body validation, so a 400 on the body can never leak a
// private widget's existence to a non-creator. Unknown keys are 400;
// values are clamped, never rejected.
func (h *DashboardHandler) updateWidgetLayout(c *echo.Context) error {
	widgetID, ok := requireUUIDParam(c, "widget_id", "widget id")
	if !ok {
		return nil
	}
	if _, err := service.AuthorizeWidgetWrite(c.Request().Context(), h.Pool, widgetID, CurrentUser(c).ID); err != nil {
		return dashboardError(c, err)
	}
	var raw map[string]json.RawMessage
	if err := c.Bind(&raw); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	var in service.UpdateWidgetLayoutInput
	for k, v := range raw {
		if string(v) == "null" {
			continue // explicit null = absent
		}
		var n int
		if err := json.Unmarshal(v, &n); err != nil {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid layout: grid values must be integers", nil)
		}
		switch k {
		case "grid_x":
			in.GridX = &n
		case "grid_y":
			in.GridY = &n
		case "grid_w":
			in.GridW = &n
		case "grid_h":
			in.GridH = &n
		default:
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid layout: only grid_x, grid_y, grid_w, grid_h are accepted", nil)
		}
	}
	w, err := service.UpdateWidgetLayout(c.Request().Context(), h.Pool, widgetID, CurrentUser(c).ID, in)
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusOK, w)
}

// deleteWidget implements DELETE /api/v1/widgets/{widget_id}.
func (h *DashboardHandler) deleteWidget(c *echo.Context) error {
	widgetID, ok := requireUUIDParam(c, "widget_id", "widget id")
	if !ok {
		return nil
	}
	if err := service.DeleteWidget(c.Request().Context(), h.Pool, widgetID, CurrentUser(c).ID); err != nil {
		return dashboardError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// getWidgetData implements GET /api/v1/widgets/{widget_id}/data: the
// live recompute.
//
// Response shapes (the frontend builds to this contract):
//   - bar|line|area|donut|pie: {groups:[{key,label,value}]}
//   - number: {value}
//   - smart_counter|table_2d: {groups:[{key,label,value}], page, max_rows, total_groups}
//     (groups holds only the requested page — absent when the page is
//     empty; smart_counter honors the config's visualization grid|table
//     at render time)
//   - smart_gauge: {groups:[{key,label,value,completed,percentage}], page, max_rows, total_groups}
//     (value = the group's total — issue count, or summed estimate
//     points when metric=estimate_points; completed = the completed
//     share — issues in a 'completed'-group state or their points;
//     percentage = 100*completed/value, 0 when value is 0)
//   - intake_breakdown: {intake:{in_app_volume,email_volume,form_volume,
//     total_accepted,total_rejected,avg_decide_seconds,
//     total_duplicate,pending,snoozed}}
//     (email_volume is always 0 — glance has no email ingestion;
//     pending uses the inbox EffectiveStatus rule, snoozed is
//     still-snoozed only)
//   - work_items_table|assigned_to_me: {rows:[{id,title,state,
//     state_group,priority,assignee:{id,name}|null,labels:[{id,name,
//     color}],project:{id,name}}], page, max_rows, total} (rows holds
//     only the requested page, in the config's sort order; the full row
//     is always returned — the configured columns only decide what the
//     frontend renders; assigned_to_me forces assignee = the caller at
//     compute time)
//   - pql_table: the same rows shape, for the rows matching the
//     stored PQL query translated once per pinned project and UNION
//     ALLed with ONE global ORDER BY (the query's ORDER BY
//     (multi-key) wins over the config sort, and the query's LIMIT
//     caps the TOTAL rows across pages and projects (total is the
//     capped count)). A stored query whose names no longer resolve
//     (a renamed state/epic/label) is a per-widget 400 on this route
//     with the PQL message + position in details, annotated with the
//     failing project's name when 2+ projects are pinned — the rest
//     of the dashboard still loads. A project the viewer cannot
//     reach contributes zero rows, not an error. The config itself
//     rejects a blank query, a bad parse at write time, 0 pinned
//     projects, and any filters key (PQL is the filter).
//
// The paginated types take ?page= (default 1, must be >= 1).
// Read authZ = the dashboard's viewer rule (member 15+; a private
// dashboard of another creator without a share is 404).
func (h *DashboardHandler) getWidgetData(c *echo.Context) error {
	widgetID, ok := requireUUIDParam(c, "widget_id", "widget id")
	if !ok {
		return nil
	}
	page := 1
	if raw := c.QueryParam("page"); raw != "" {
		p, err := strconv.Atoi(raw)
		if err != nil || p < 1 {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid page: want an integer >= 1", nil)
		}
		page = p
	}
	data, err := service.GetWidgetData(c.Request().Context(), h.Pool, widgetID, CurrentUser(c).ID, page)
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusOK, data)
}

// getWidgetDrilldown implements GET
// /api/v1/widgets/{widget_id}/drilldown — two shapes, dispatched on
// the widget's type (authZ runs first, so a private unshared widget
// is 404 before any metric/group validation can leak it):
//
//   - Grouped widgets (bar|line|area|donut|pie|smart_counter|
//     smart_gauge|table_2d): ?group=<group-key>&page=... — the items
//     behind one chart segment. The group key is the widget's own
//     human-label group key — the Key or the Label getWidgetData
//     returns for the segment (state name, priority label, assignee
//     display name / "Unassigned", label name / "No label",
//     state-group display name, project name, creator display name).
//     The backend maps it back to the dimension's filter value with
//     the SAME resolution the data compute uses (never a guess): an
//     unresolvable key is 400. group is required (400 when missing).
//     A ?metric= param is meaningless here and ignored. Response:
//     {items:[{id,title,state,state_group,priority,
//     assignee:{id,name}|null,labels:[{id,name,color}],project:{id,name}}],
//     page, per_page (the widget's max_rows), total, group_label,
//     group_by}. Items are most-recently-updated first.
//
//   - intake_breakdown widgets (C85): ?metric=<metric>&page=...
//     &per_page=... — the rows behind one tile. metric ∈ in_app|form|
//     email|accepted|rejected|duplicate|pending|snoozed and is
//     required (400 when missing); avg_decide is 400 (an aggregate,
//     not a row set); unknown metrics are 400. A ?group= param is
//     meaningless for intake tiles and is ignored. Scope = every
//     non-archived project of the workspace (the
//     computeIntakeBreakdown scope — widget-level filters and
//     project_ids stay ignored). pending = status pending +
//     resurfaced snoozed (the inbox EffectiveStatus rule); snoozed =
//     still-snoozed only; email = an empty list (glance has no email
//     ingestion — rows are never faked). per_page defaults to the
//     widget's max_rows (the C84 drill-down convention — default 10)
//     and is capped at 100 (400 past it). Response:
//     {items:[{id,issue_id,title,project:{id,name},status,
//     status_name,source,snoozed_till,duplicate_to_id,created_at}],
//     page, per_page, total, metric} — status/status_name carry the
//     EFFECTIVE status. Items are most-recently-updated first.
//
//   - C87 bucket window on intake_breakdown widgets:
//     ?granularity=day|week|month&bucket_start=YYYY-MM-DD — the
//     rows created in one trend bucket (the trend-chart click).
//     Both params or neither: one without the other is 400;
//     unknown granularity, malformed date, or a misaligned start
//     (not a Monday for week, not the 1st for month) is 400.
//     Windows are half-open UTC calendar periods: day
//     [start, start+1d); week [Monday, Monday+7d); month [1st,
//     next 1st). The bucket ANDs onto the metric predicate when
//     metric is given; without metric it lists every row created
//     in the bucket (any source/status) and the response carries
//     metric "" — metric is optional only with a bucket. pending/
//     snoozed keep their C85 EFFECTIVE-status definitions AND
//     the creation-bucket filter (a row counts only if both
//     hold — the honest reading, not a point-in-time snapshot).
//     total counts the bucket-filtered rows; granularity and
//     bucket_start are echoed only when the bucket filter is
//     present (absent otherwise). Grouped-widget drill-down
//     ignores granularity/bucket_start entirely (no 400 on the
//     ignored params, matching the C85 group/metric ignores).
//
// Types without any drill-down (number, work_items_table,
// assigned_to_me, pql_table — the rows already ARE the list) are 400
// — drill-down is not available for them.
// Read authZ = the widget read rule (a shared-view member may drill;
// a private dashboard of another creator without a share is 404).
func (h *DashboardHandler) getWidgetDrilldown(c *echo.Context) error {
	widgetID, ok := requireUUIDParam(c, "widget_id", "widget id")
	if !ok {
		return nil
	}
	page := 1
	if raw := c.QueryParam("page"); raw != "" {
		p, err := strconv.Atoi(raw)
		if err != nil || p < 1 {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid page: want an integer >= 1", nil)
		}
		page = p
	}
	widgetType, err := service.GetWidgetType(c.Request().Context(), h.Pool, widgetID, CurrentUser(c).ID)
	if err != nil {
		return dashboardError(c, err)
	}
	if widgetType == "intake_breakdown" {
		perPage := 0 // 0 = the widget's max_rows (service fallback)
		if raw := c.QueryParam("per_page"); raw != "" {
			p, err := strconv.Atoi(raw)
			if err != nil || p < 1 || p > 100 {
				return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid per_page: want 1-100", nil)
			}
			perPage = p
		}
		data, err := service.GetIntakeDrilldown(c.Request().Context(), h.Pool, widgetID, CurrentUser(c).ID, c.QueryParam("metric"), c.QueryParam("granularity"), c.QueryParam("bucket_start"), page, perPage)
		if err != nil {
			return dashboardError(c, err)
		}
		return c.JSON(http.StatusOK, data)
	}
	data, err := service.GetWidgetDrilldown(c.Request().Context(), h.Pool, widgetID, CurrentUser(c).ID, c.QueryParam("group"), page)
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusOK, data)
}

// parseTrendParams parses the shared trend query params
// (?granularity= + ?buckets=) for the trend endpoint and the trend
// CSV export. Absent params carry the defaults (week, 12) — the
// handler can tell "absent" from "explicitly 0", so an explicit
// buckets=0 reaches the service and fails as out of range instead
// of silently becoming the default. A non-integer buckets value is
// a 400 here (syntax); range validation (1-52) and granularity
// validation live in the service, after authZ, so a private
// unshared widget still reads 404 for a semantically invalid but
// syntactically well-formed request.
func parseTrendParams(c *echo.Context) (string, int, bool) {
	buckets := 12
	if raw := c.QueryParam("buckets"); raw != "" {
		b, err := strconv.Atoi(raw)
		if err != nil {
			WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid buckets: want 1-52", nil)
			return "", 0, false
		}
		buckets = b
	}
	return c.QueryParam("granularity"), buckets, true
}

// getWidgetTrend implements GET /api/v1/widgets/{widget_id}/trend —
// the time-bucketed intake series for an intake_breakdown widget:
// ?granularity=day|week|month (default week; unknown → 400) and
// ?buckets=1-52 (default 12; out of range → 400). Buckets are
// consecutive periods ending at the current period (the bucket
// containing now), so the series always ends "now". Week buckets
// start Monday (ISO week); month buckets are calendar months; day
// buckets are calendar days (UTC). Per bucket: bucket_start (ISO
// date of the bucket's first day), in_app, form, email (always 0 —
// glance has no email ingestion), accepted, rejected, duplicate —
// all counted by the row's created_at falling in the bucket. Decided
// counts are by CREATION bucket, not decision time: a row decided
// this week but created last month counts in last month's bucket,
// keeping created volume vs decided volume comparable within a
// bucket (triage lag reads as decided < created in recent buckets).
// Scope = every non-archived project of the workspace (widget-level
// filters and project_ids ignored, like the headline numbers).
// Empty buckets render as zeros (no gaps). Response:
// {granularity, buckets:[{bucket_start,in_app,form,email,accepted,
// rejected,duplicate}...], total_buckets}. A non-intake_breakdown
// widget is an honest 400. Read authZ = the widget read rule (a
// shared-view member may fetch; a private dashboard of another
// creator without a share is 404).
func (h *DashboardHandler) getWidgetTrend(c *echo.Context) error {
	widgetID, ok := requireUUIDParam(c, "widget_id", "widget id")
	if !ok {
		return nil
	}
	granularity, buckets, ok := parseTrendParams(c)
	if !ok {
		return nil
	}
	data, err := service.GetIntakeTrend(c.Request().Context(), h.Pool, widgetID, CurrentUser(c).ID, granularity, buckets)
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusOK, data)
}

// exportWidgetCSV implements GET /api/v1/widgets/{widget_id}/export.csv:
// the widget's FULL live dataset as CSV (same recompute as the data
// endpoint, but unpaginated — every group / every row, not just the
// current page), capped at 10,000 data rows (past the cap the response
// carries X-Result-Truncated: true). Columns per type:
// key,label,value (charts, smart_counter, table_2d);
// key,label,completed,total,percentage (smart_gauge);
// metric,value (intake_breakdown aggregate); value (number); the configured
// columns in order (work_items_table, assigned_to_me, pql_table —
// for pql_table a query LIMIT caps the export rows exactly as it
// caps the data pages). For
// intake_breakdown widgets, ?metric=<metric> dumps the drill-down row
// set instead (id,issue_id,title,project,status,status_name,source,
// snoozed_till,duplicate_to_id,created_at — same predicate and order
// as the drill-down, same 10,000-row cap + X-Result-Truncated);
// without metric the aggregate export is unchanged. A metric on a
// non-intake widget is ignored; avg_decide/unknown metrics on intake
// widgets are 400. With ?trend=1 on intake_breakdown widgets the
// export is the trend series instead (columns bucket_start,in_app,
// form,email,accepted,rejected,duplicate; + granularity + buckets
// params, same validation as the trend endpoint; the 10,000-row cap
// never binds on a ≤52-row series, so X-Result-Truncated is never
// set); ?trend=1 on a non-intake widget is 400. Content-Disposition:
// attachment; filename="<dashboard-slug>-<widget-slug>.csv" (sanitized).
// C88 bucket window (non-trend path only — the ?trend=1 path never
// reads bucket_start): on intake_breakdown widgets,
// ?granularity=day|week|month&bucket_start=YYYY-MM-DD exports the
// bucket-filtered drill-down row set instead of the aggregate
// (bucket + metric → that metric's rows created in the bucket;
// bucket without metric → all rows created in the bucket; same
// columns, same 10,000-row cap + X-Result-Truncated, same scope —
// workspace, non-archived projects, widget filters ignored).
// Both-or-neither, unknown granularity, malformed date, and
// misaligned starts are 400 (validated in the service by
// parseIntakeDrilldownBucket, after authZ, so a private unshared
// widget is 404 first). Filename with a bucket:
// "<dashboard-slug>-<widget-slug>-<granularity>-<bucket_start>.csv"
// (e.g. "...-week-2026-10-26.csv") — no metric segment, because
// existing metric exports carry none, so the bucket suffix alone
// distinguishes a bucket file from a metric file and a full file.
// On non-intake and grouped widgets the bucket params are ignored
// entirely (never 400 on the ignored param, C87 convention).
// Read authZ = the widget read rule (a shared-view member may export).
func (h *DashboardHandler) exportWidgetCSV(c *echo.Context) error {
	widgetID, ok := requireUUIDParam(c, "widget_id", "widget id")
	if !ok {
		return nil
	}
	// C86T0: ?trend=1 dumps the trend series instead of the
	// aggregate/metric export (same granularity + buckets params and
	// validation as the trend endpoint). Any other trend value falls
	// through to the existing export paths, unchanged.
	if c.QueryParam("trend") == "1" {
		granularity, buckets, ok := parseTrendParams(c)
		if !ok {
			return nil
		}
		filename, data, err := service.ExportWidgetTrendCSV(c.Request().Context(), h.Pool, widgetID, CurrentUser(c).ID, granularity, buckets)
		if err != nil {
			return dashboardError(c, err)
		}
		c.Response().Header().Set("Content-Type", "text/csv")
		c.Response().Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		// No X-Result-Truncated: the series is at most 52 rows, so
		// the 10,000-row cap never binds.
		return c.Blob(http.StatusOK, "text/csv", data)
	}
	// C88T0: bucket params are read ONLY on the non-trend path —
	// the ?trend=1 branch above never sees them. Validation lives
	// in the service (after authZ; ignored on non-intake widgets).
	filename, data, truncated, err := service.ExportWidgetCSV(c.Request().Context(), h.Pool, widgetID, CurrentUser(c).ID, c.QueryParam("metric"), c.QueryParam("granularity"), c.QueryParam("bucket_start"))
	if err != nil {
		return dashboardError(c, err)
	}
	c.Response().Header().Set("Content-Type", "text/csv")
	c.Response().Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	if truncated {
		c.Response().Header().Set("X-Result-Truncated", "true")
	}
	return c.Blob(http.StatusOK, "text/csv", data)
}

// getDashboardShares implements GET
// /api/v1/workspaces/{slug}/dashboards/{id}/shares: the dashboard's
// share rows with member display names, oldest first. Owner-only —
// shared members cannot even read the share list (no re-share), and a
// private dashboard invisible to the caller is 404.
func (h *DashboardHandler) getDashboardShares(c *echo.Context) error {
	id, ok := requireUUIDParam(c, "id", "dashboard id")
	if !ok {
		return nil
	}
	shares, err := service.GetDashboardShares(c.Request().Context(), h.Pool, c.Param("slug"), id, CurrentUser(c).ID)
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"shares": shares})
}

type dashboardShareBody struct {
	MemberID string `json:"member_id"`
	Access   string `json:"access"`
}

type replaceDashboardSharesBody struct {
	Shares []dashboardShareBody `json:"shares"`
}

// replaceDashboardShares implements PUT
// /api/v1/workspaces/{slug}/dashboards/{id}/shares — Plane's one-batch
// model: the body carries the WHOLE desired share list
// [{member_id, access}], replaced in one transaction (add/change/
// remove); an empty/absent list clears every share. access ∈
// view|edit. Only the dashboard creator may manage shares; the creator
// themselves and guests can never be added (400).
func (h *DashboardHandler) replaceDashboardShares(c *echo.Context) error {
	id, ok := requireUUIDParam(c, "id", "dashboard id")
	if !ok {
		return nil
	}
	var body replaceDashboardSharesBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	in := make([]service.ShareInput, 0, len(body.Shares))
	for _, s := range body.Shares {
		in = append(in, service.ShareInput{MemberID: s.MemberID, Access: s.Access})
	}
	shares, err := service.ReplaceDashboardShares(c.Request().Context(), h.Pool, c.Param("slug"), id, CurrentUser(c).ID, in)
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"shares": shares})
}

// listDashboardTemplates implements GET
// /api/v1/workspaces/{slug}/dashboard-templates: the static template
// catalog [{id, name, description, widgets:[{title, widget_type,
// config, grid_x, grid_y, grid_w, grid_h}]}]. Member (15)+; guests 403.
func (h *DashboardHandler) listDashboardTemplates(c *echo.Context) error {
	templates, err := service.ListDashboardTemplates(c.Request().Context(), h.Pool, c.Param("slug"), CurrentUser(c).ID)
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"templates": templates})
}
