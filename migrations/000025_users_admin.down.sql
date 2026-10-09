-- 000025_users_admin down: drop what the up migration added.
-- Dropping the column also drops the default; there is no index or
-- constraint to clean up.
ALTER TABLE users DROP COLUMN IF EXISTS is_admin;
