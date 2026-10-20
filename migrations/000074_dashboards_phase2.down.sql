-- 000074_dashboards_phase2 down: drop the phase-2 surface (manual
-- rollback only; the migrator never applies down files).
--
-- Caveat: restoring the phase-1 widget_type CHECK fails while any
-- phase-2 widget rows exist — delete those widgets first.

DROP TABLE IF EXISTS dashboard_shares;

ALTER TABLE intake_issues DROP COLUMN IF EXISTS source;

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
    CHECK (widget_type IN ('bar', 'line', 'area', 'donut', 'pie', 'number'));
