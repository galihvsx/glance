-- 000072_dashboards: customizable dashboards + widgets (C81T0).
--
-- Plane's Dashboards (docs.plane.so/dashboards) are workspace-scoped
-- data-visualization canvases: a 12-column grid of widgets, each
-- independently configured (chart type, grouping, filters, project
-- scope) and recomputed live from work-item data. Visibility is public
-- (default: visible to all workspace members; only the creator and
-- workspace admins/owners can edit/delete) or private (visible only to
-- the creator).
--
-- Storage:
--   dashboards: workspace-scoped (workspace_id ON DELETE CASCADE).
--     visibility is CHECK-constrained to 'public'|'private'.
--   dashboard_widgets: dashboard-scoped (dashboard_id ON DELETE
--     CASCADE). widget_type is CHECK-constrained to the six Plane chart
--     types; config is schemaless JSONB validated at write time by the
--     service layer (group_by enum, metric reserved "count", filter
--     UUIDs, project_ids); grid_* are 12-column grid units.
--
-- Idempotent: safe to re-run (IF NOT EXISTS everywhere).

CREATE TABLE IF NOT EXISTS dashboards (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    description  TEXT,
    visibility   TEXT NOT NULL DEFAULT 'public'
                 CHECK (visibility IN ('public', 'private')),
    created_by   UUID NOT NULL REFERENCES users(id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Lookup: "dashboards of a workspace".
CREATE INDEX IF NOT EXISTS dashboards_workspace_idx
    ON dashboards (workspace_id);

CREATE TABLE IF NOT EXISTS dashboard_widgets (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dashboard_id UUID NOT NULL REFERENCES dashboards(id) ON DELETE CASCADE,
    title        TEXT NOT NULL,
    widget_type  TEXT NOT NULL
                 CHECK (widget_type IN ('bar', 'line', 'area', 'donut', 'pie', 'number')),
    config       JSONB NOT NULL DEFAULT '{}',
    grid_x       INT NOT NULL DEFAULT 0,
    grid_y       INT NOT NULL DEFAULT 0,
    grid_w       INT NOT NULL DEFAULT 6,
    grid_h       INT NOT NULL DEFAULT 4,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Lookup: "widgets of a dashboard".
CREATE INDEX IF NOT EXISTS dashboard_widgets_dashboard_idx
    ON dashboard_widgets (dashboard_id);
