-- 000072_dashboards down: drop the dashboards tables (manual rollback
-- only; the migrator never applies down files). Widgets drop with their
-- dashboard via ON DELETE CASCADE.

DROP TABLE IF EXISTS dashboard_widgets;
DROP TABLE IF EXISTS dashboards;
