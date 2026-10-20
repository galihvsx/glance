package api

import (
	"encoding/json"
	"errors"
	"net/http"
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

	wg := e.Group("/api/v1/widgets", RequireAuth(h.Pool))
	wg.PATCH("/:widget_id", h.updateWidget)
	wg.DELETE("/:widget_id", h.deleteWidget)
	// C81T0: layout-only commit for drag/resize — the canvas commits
	// grid_x/grid_y/grid_w/grid_h without resending config.
	wg.PATCH("/:widget_id/layout", h.updateWidgetLayout)
	wg.GET("/:widget_id/data", h.getWidgetData)
}

// dashboardError maps dashboard/widget sentinel errors to HTTP statuses,
// delegating the shared sentinels (ErrNotFound, ErrForbidden,
// ErrNameRequired, ErrNothingToUpdate) to issueError. ErrInvalidWidgetConfig
// carries a wrapped detail ("service: invalid widget config: <detail>");
// the prefix is stripped so the message stays client-shaped.
func dashboardError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrDashboardNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "dashboard not found", nil)
	case errors.Is(err, service.ErrWidgetNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "widget not found", nil)
	case errors.Is(err, service.ErrInvalidVisibility):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, `invalid visibility: want "public" or "private"`, nil)
	case errors.Is(err, service.ErrInvalidWidgetType):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid widget_type: want one of bar|line|area|donut|pie|number", nil)
	case errors.Is(err, service.ErrInvalidWidgetConfig):
		msg := strings.TrimPrefix(err.Error(), "service: invalid widget config: ")
		msg = strings.TrimPrefix(msg, "service: invalid widget config")
		if msg == "" {
			msg = "invalid widget config"
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
}

type updateDashboardBody struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Visibility  *string `json:"visibility"`
}

// listDashboards implements GET /api/v1/workspaces/{slug}/dashboards:
// public dashboards plus the caller's own private ones, newest updated
// first. Member (15)+; guests 403; bad slug / non-member 404.
func (h *DashboardHandler) listDashboards(c *echo.Context) error {
	dashboards, err := service.ListDashboards(c.Request().Context(), h.Pool, c.Param("slug"), CurrentUser(c).ID)
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"dashboards": dashboards})
}

// createDashboard implements POST /api/v1/workspaces/{slug}/dashboards.
// visibility defaults to public; anything else is 400.
func (h *DashboardHandler) createDashboard(c *echo.Context) error {
	var body createDashboardBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
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
// /api/v1/workspaces/{slug}/dashboards/{id} under Plane's edit rule:
// creator, or admin on public dashboards; private dashboards creator
// only (everyone else sees 404).
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
// live recompute — {groups:[{key,label,value}]} for the five chart
// types, {value} for number widgets. Read authZ = the dashboard's
// viewer rule (member 15+; private dashboard of another creator → 404).
func (h *DashboardHandler) getWidgetData(c *echo.Context) error {
	widgetID, ok := requireUUIDParam(c, "widget_id", "widget id")
	if !ok {
		return nil
	}
	data, err := service.GetWidgetData(c.Request().Context(), h.Pool, widgetID, CurrentUser(c).ID)
	if err != nil {
		return dashboardError(c, err)
	}
	return c.JSON(http.StatusOK, data)
}
