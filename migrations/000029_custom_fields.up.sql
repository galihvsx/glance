-- 000029_custom_fields: project-scoped custom fields (C7T2).
--
-- A custom field is a typed key an issue can carry a value for.
-- field_type is one of 'text' | 'number' | 'date' | 'select' | 'checkbox'.
-- options is a JSON array for select fields: [{value, color?}]; it is
-- shape-validated by the service on write and defaults to '[]'.
-- Values live in issue_custom_values (000030), which cascades with the
-- field. IF NOT EXISTS so the migration is re-runnable.
CREATE TABLE IF NOT EXISTS custom_fields (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id  UUID NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    field_type  TEXT NOT NULL
        CHECK (field_type IN ('text', 'number', 'date', 'select', 'checkbox')),
    options     JSONB NOT NULL DEFAULT '[]',
    required    BOOL NOT NULL DEFAULT false,
    position    INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_custom_fields UNIQUE (project_id, name)
);

CREATE INDEX IF NOT EXISTS idx_custom_fields_project
    ON custom_fields (project_id, position, created_at);
