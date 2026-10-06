-- 000005_workspace: workspaces + membership (spec §4, Task 11).
--
-- slug is CITEXT UNIQUE: case-insensitive uniqueness, so "Acme" and "acme"
-- can never coexist. role is SMALLINT: 20=admin, 15=member, 5=guest.
-- Membership is the tenancy boundary: every Phase 2+ query joins through
-- workspace_members so a user only ever sees their own workspaces.
-- citext extension was created in 000003_auth; IF NOT EXISTS keeps this
-- migration self-contained and re-runnable.
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE IF NOT EXISTS workspaces (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug       CITEXT NOT NULL UNIQUE,
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS workspace_members (
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role         SMALLINT NOT NULL CHECK (role IN (5, 15, 20)),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, user_id)
);
-- Reverse lookup: "which workspaces does this user belong to" (list view).
CREATE INDEX IF NOT EXISTS workspace_members_user_idx ON workspace_members (user_id);
