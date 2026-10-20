-- 000073_intake_forms down: drop the intake forms table (manual rollback
-- only; the migrator never applies down files). The system user row stays:
-- it is inert (inactive, unusable for auth) and other rows may reference it.
DROP TABLE IF EXISTS intake_forms;
