-- 000080_dashboards_pql_widget: PQL-driven row-list widget (C100T0).
--
-- Adds one widget type on top of the twelve phase-1+2+3 types:
-- pql_table, a row list driven by a PQL query string (same row shape,
-- columns, and pagination as work_items_table). The widget_type CHECK
-- is widened exactly the way 000074/000075 did it: the current
-- constraint name is looked up by definition (it is named
-- dashboard_widgets_widget_type_check since 000074, but the lookup is
-- defensive against future renames), then the widened CHECK is
-- re-added under the canonical name.
--
-- No storage beyond the type: the query lives in the widget's config
-- JSON (query, project_ids, max_rows, columns, sort_by, sort_dir),
-- validated in the service layer like every other widget type.
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
                           'assigned_to_me', 'pql_table'));
