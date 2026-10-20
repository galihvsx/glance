-- 000073_intake_forms: per-project custom intake forms (C82T0).
--
-- An intake form is a shareable, unauthenticated submission surface for a
-- project's intake inbox. fields is an ORDERED jsonb array of descriptors
-- { "key": string, "label": string, "required": bool } where key is one of
--   title | description | priority | custom:<custom_field_uuid>.
-- Field-shape validation lives in the service layer (it needs the project's
-- custom_fields, which SQL cannot see); the column is jsonb, never NULL.
--
-- share_token is the public capability (crypto-random 32 bytes, base64url,
-- UNIQUE backstop). intake_id is always the project's default intake at
-- creation time (resolved by the service, not invented per form).
--
-- Public submissions are attributed to a dedicated, inactive system user
-- under the reserved SystemUserID below — never to a random member. issues.created_by and
-- issue_activities.actor_id are NOT NULL FKs to users(id), so anonymous
-- creation needs a real row; seeding it here makes it visible and
-- auditable instead of lazily inventing one in code. The row is inactive
-- (it can never pass OTP auth) and its email is reserved.
CREATE TABLE IF NOT EXISTS intake_forms (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id  UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    intake_id   UUID NOT NULL REFERENCES intake (id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    description TEXT,
    is_enabled  BOOL NOT NULL DEFAULT true,
    share_token TEXT NOT NULL UNIQUE,
    fields      JSONB NOT NULL DEFAULT '[]',
    created_by  UUID NOT NULL REFERENCES users (id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_intake_forms_project
    ON intake_forms (project_id, created_at);

-- The system user id is a fixed reserved UUID — deliberately NOT the nil
-- UUID, which the test suite uses as its guaranteed-unknown-user sentinel.
INSERT INTO users (id, email, name, is_active)
VALUES ('4451ef68-06f5-4bc7-80db-dc0aec4c9d67', 'system@glance.internal', 'Intake forms', false)
ON CONFLICT (id) DO NOTHING;
