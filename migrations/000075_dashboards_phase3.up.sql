-- 000075_dashboards_phase3: phase-3 dashboard surface (C84T0).
--
-- Phase 3 adds two row-list widget types on top of the ten phase-1+2
-- types: work_items_table (a configurable, sortable, paginated work-
-- item row list) and assigned_to_me (the same row list pre-filtered
-- to assignee = the caller). The widget_type CHECK is widened exactly
-- the way 000074 did it: the current constraint name is looked up by
-- definition (it is named dashboard_widgets_widget_type_check since
-- 000074, but the lookup is defensive against future renames), then
-- the widened CHECK is re-added under the canonical name.
--
-- The drill-down endpoint (GET /api/v1/widgets/:id/drilldown) needs
-- no storage: it recomputes group segments live like getWidgetData.
--
-- Idempotent: safe to re-run (the constraint drop is looked up by
-- definition; the ADD is not IF NOT EXISTS but re-running after a
-- successful apply is a no-op only via the migration ledger — the
-- migrator never re-applies an applied migration).
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
                           'intake_breakdown', 'work_items_table',
                           'assigned_to_me'));
