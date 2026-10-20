package service

// Dashboard templates (C83T0): the static create-from-template catalog.
//
// Templates are fixed definitions — there is deliberately no templates
// table (per the cycle-83 plan). Each template is a dashboard name,
// description, and an ordered widget list; creating from one inserts
// the dashboard (visibility public, creator = caller) plus every
// widget in ONE transaction. Every template widget config below is
// validated by parseWidgetConfig at creation time, so a template that
// drifts out of the validation contract fails loudly instead of
// half-applying.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DashboardTemplateWidget is one widget of a dashboard template: the
// same fields as a create-widget body (title, type, config, grid).
type DashboardTemplateWidget struct {
	Title      string          `json:"title"`
	WidgetType string          `json:"widget_type"`
	Config     json.RawMessage `json:"config"`
	GridX      int             `json:"grid_x"`
	GridY      int             `json:"grid_y"`
	GridW      int             `json:"grid_w"`
	GridH      int             `json:"grid_h"`
}

// DashboardTemplate is one entry of the template catalog.
type DashboardTemplate struct {
	ID          string                    `json:"id"`
	Name        string                    `json:"name"`
	Description string                    `json:"description"`
	Widgets     []DashboardTemplateWidget `json:"widgets"`
}

// dashboardTemplates is the static catalog. Widget configs use only
// keys their type accepts (see parseWidgetConfig).
var dashboardTemplates = []DashboardTemplate{
	{
		ID:          "sprint-health",
		Name:        "Sprint health",
		Description: "Track sprint progress at a glance: completion gauges per state group and a workload counter per assignee.",
		Widgets: []DashboardTemplateWidget{
			{
				Title:      "Completion by state group",
				WidgetType: WidgetTypeSmartGauge,
				Config:     json.RawMessage(`{"group_by":"state_group","metric":"count","max_rows":10,"gauge_color":"#3b82f6","show_percentage":true}`),
				GridX:      0, GridY: 0, GridW: 6, GridH: 4,
			},
			{
				Title:      "Workload by assignee",
				WidgetType: WidgetTypeSmartCounter,
				Config:     json.RawMessage(`{"group_by":"assignee","metric":"count","max_rows":10,"visualization":"grid"}`),
				GridX:      6, GridY: 0, GridW: 6, GridH: 4,
			},
		},
	},
	{
		ID:          "triage-performance",
		Name:        "Triage performance",
		Description: "Monitor intake health: where work items come from, how fast they are decided, and the pending load by priority.",
		Widgets: []DashboardTemplateWidget{
			{
				Title:      "Intake breakdown",
				WidgetType: WidgetTypeIntake,
				Config:     json.RawMessage(`{"view":"grid"}`),
				GridX:      0, GridY: 0, GridW: 12, GridH: 4,
			},
			{
				Title:      "Intake by priority",
				WidgetType: WidgetTypeTable2D,
				Config:     json.RawMessage(`{"group_by":"priority","max_rows":10}`),
				GridX:      0, GridY: 4, GridW: 6, GridH: 4,
			},
		},
	},
	{
		ID:          "project-overview",
		Name:        "Project overview",
		Description: "The classic status board: issues by state, issues by priority, and the live open-issue count.",
		Widgets: []DashboardTemplateWidget{
			{
				Title:      "Issues by state",
				WidgetType: WidgetTypeBar,
				Config:     json.RawMessage(`{"group_by":"state"}`),
				GridX:      0, GridY: 0, GridW: 6, GridH: 4,
			},
			{
				Title:      "Issues by priority",
				WidgetType: WidgetTypeDonut,
				Config:     json.RawMessage(`{"group_by":"priority"}`),
				GridX:      6, GridY: 0, GridW: 6, GridH: 4,
			},
			{
				Title:      "Open issues",
				WidgetType: WidgetTypeNumber,
				Config:     json.RawMessage(`{}`),
				GridX:      0, GridY: 4, GridW: 12, GridH: 3,
			},
		},
	},
}

// DashboardTemplateCatalog returns the static template catalog.
func DashboardTemplateCatalog() []DashboardTemplate {
	return dashboardTemplates
}

// lookupDashboardTemplate finds a template by id.
func lookupDashboardTemplate(id string) (*DashboardTemplate, bool) {
	for i := range dashboardTemplates {
		if dashboardTemplates[i].ID == id {
			return &dashboardTemplates[i], true
		}
	}
	return nil, false
}

// ListDashboardTemplates returns the template catalog. Workspace
// member (15)+; guests ErrForbidden, bad slug / non-member ErrNotFound.
func ListDashboardTemplates(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID string) ([]DashboardTemplate, error) {
	if _, _, err := resolveDashboardWorkspace(ctx, pool, wsSlug, actorID); err != nil {
		return nil, err
	}
	return DashboardTemplateCatalog(), nil
}

// CreateDashboardFromTemplate materializes a template: the dashboard
// (visibility public, creator = caller, name defaulting to the
// template name) plus every template widget, in ONE transaction. An
// unknown template id is ErrUnknownDashboardTemplate (400). Every
// template widget config is re-validated before the tx opens, so a
// template that drifts out of the validation contract fails loudly
// instead of half-applying.
func CreateDashboardFromTemplate(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, templateID, name string) (*Dashboard, error) {
	tpl, ok := lookupDashboardTemplate(templateID)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownDashboardTemplate, templateID)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = tpl.Name
	}
	wsID, _, err := resolveDashboardWorkspace(ctx, pool, wsSlug, actorID)
	if err != nil {
		return nil, err
	}
	for i := range tpl.Widgets {
		tw := &tpl.Widgets[i]
		if !validWidgetType(tw.WidgetType) {
			return nil, fmt.Errorf("dashboard template %q widget %q: %w", tpl.ID, tw.Title, ErrInvalidWidgetType)
		}
		if _, err := parseWidgetConfig(tw.WidgetType, tw.Config); err != nil {
			return nil, fmt.Errorf("dashboard template %q widget %q: %w", tpl.ID, tw.Title, err)
		}
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var dashboardID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO dashboards (workspace_id, name, visibility, created_by)
		 VALUES ($1::uuid, $2, 'public', $3::uuid)
		 RETURNING id::text`,
		wsID, name, actorID).Scan(&dashboardID); err != nil {
		return nil, err
	}
	for i := range tpl.Widgets {
		tw := &tpl.Widgets[i]
		x, y, w, h := clampGrid(tw.GridX, tw.GridY, tw.GridW, tw.GridH)
		configJSON := tw.Config
		if len(configJSON) == 0 {
			configJSON = json.RawMessage("{}")
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO dashboard_widgets (dashboard_id, title, widget_type, config, grid_x, grid_y, grid_w, grid_h)
			 VALUES ($1::uuid, $2, $3, $4::jsonb, $5, $6, $7, $8)`,
			dashboardID, tw.Title, tw.WidgetType, string(configJSON), x, y, w, h); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return GetDashboard(ctx, pool, wsSlug, dashboardID, actorID)
}
