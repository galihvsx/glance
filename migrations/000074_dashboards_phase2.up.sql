-- 000074_dashboards_phase2: phase-2 dashboard surface (C83T0).
--
-- Plane's newer Dashboards (docs.plane.so/dashboards, changelog
-- 2026-08-31) add four widget types on top of the six phase-1 charts:
-- smart counter (grouped count / estimate-points tiles with pagination),
-- smart gauge (per-group completion arcs), a two-dimensional table
-- (group × count), and an intake breakdown (in-app / form / email
-- volume plus accepted / rejected / time-to-decide). Dashboards can
-- also be shared with selected teammates (owner-managed, View/Edit
-- access), created from a template catalog, and exported per widget
-- as CSV.
--
-- Storage:
--   dashboard_widgets.widget_type CHECK widened with the four new
--     types (the 000072 inline CHECK was auto-named by Postgres —
--     looked up by definition, never assumed).
--   intake_issues.source: which channel created the intake row
--     ('in_app' | 'form' | 'email'). Existing rows default to
--     'in_app' (the only channel that existed before this migration).
--     'email' is reserved: glance has no email ingestion, so the
--     intake-breakdown widget reports email_volume as 0.
--   dashboard_shares: per-member access grants on a dashboard
--     (dashboard_id, member_id) UNIQUE, ON DELETE CASCADE both ways.
--     Management is owner-only and guests can never be added —
--     enforced in the service layer, not here.
--   No templates table: the template catalog is a static in-code
--     registry (templates are fixed definitions).
--
-- Idempotent: safe to re-run (IF NOT EXISTS / looked-up constraint
-- drop everywhere).

-- Widen the widget_type CHECK with the four phase-2 types.
DO $$
DECLARE
    cname text;
BEGIN
    SELECT conname INTO cname
      FROM pg_constraint
     WHERE conrelid = 'dashboard_widgets'::regclass
       AND contype = 'c'
       AND pg_get_constraintdef(oid) LIKE '%widget_type%';
    IF cname IS NOT NULL THEN
        EXECUTE 'ALTER TABLE dashboard_widgets DROP CONSTRAINT ' || quote_ident(cname);
    END IF;
END
$$;

ALTER TABLE dashboard_widgets
    ADD CONSTRAINT dashboard_widgets_widget_type_check
    CHECK (widget_type IN ('bar', 'line', 'area', 'donut', 'pie', 'number',
                           'smart_counter', 'smart_gauge', 'table_2d',
                           'intake_breakdown'));

-- Intake source tracking.
ALTER TABLE intake_issues
    ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'in_app'
    CHECK (source IN ('in_app', 'form', 'email'));

-- Per-member dashboard shares.
CREATE TABLE IF NOT EXISTS dashboard_shares (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dashboard_id UUID NOT NULL REFERENCES dashboards(id) ON DELETE CASCADE,
    member_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    access       TEXT NOT NULL CHECK (access IN ('view', 'edit')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (dashboard_id, member_id)
);
-- Lookup: "shares granted to a member" (the list endpoint's join).
CREATE INDEX IF NOT EXISTS dashboard_shares_member_idx
    ON dashboard_shares (member_id, dashboard_id);
