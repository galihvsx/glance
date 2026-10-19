-- 000069_epics down: drop the epic tables (manual rollback only; the
-- migrator never applies down files).

DROP TABLE IF EXISTS initiative_epics;
DROP TABLE IF EXISTS epic_issues;
DROP TABLE IF EXISTS epics;
