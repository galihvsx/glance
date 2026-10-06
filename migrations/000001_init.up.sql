-- 000001_init: creates the schema_migrations bookkeeping table.
-- This is the only table owned by the migration runner itself; all domain
-- tables arrive in later migrations.
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
