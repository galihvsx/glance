-- 000025_users_admin: instance-level admin flag on users (C5T0).
--
-- is_admin marks instance administrators: the only principals allowed
-- behind /api/v1/admin/*. It is deliberately separate from workspace
-- roles (member/admin/guest live in workspace_members) — workspace
-- admin is tenancy-scoped, is_admin is instance-scoped.
--
-- Seeding policy (see internal/auth: otp.go / oauth.go / boot hook in
-- cmd/glance/main.go):
--   * the first user registered on an empty users table gets is_admin=true
--     (NOT EXISTS check inside the provisioning INSERT — atomic);
--   * GLANCE_ADMIN_EMAILS (comma-separated, config.AdminEmails) is
--     applied idempotently at boot (UPDATE ... SET is_admin=true) and
--     to newly registered users whose email matches — never demotes.
--
-- IF NOT EXISTS so the migration is re-runnable.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS is_admin BOOLEAN NOT NULL DEFAULT false;
