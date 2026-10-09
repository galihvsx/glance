-- 000030_issue_custom_values: per-issue values for project custom fields
-- (C7T2).
--
-- One row per (issue, field) pair. Exactly one value_* column is non-NULL
-- per row, matching the field's type at write time:
--   text     -> value_text
--   number   -> value_number
--   date     -> value_date
--   select   -> value_text (the option value)
--   checkbox -> value_bool
-- The service type-checks values against the field before writing.
-- Rows die with the issue or the field (both ON DELETE CASCADE).
-- IF NOT EXISTS so the migration is re-runnable.
CREATE TABLE IF NOT EXISTS issue_custom_values (
    issue_id     UUID NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    field_id     UUID NOT NULL REFERENCES custom_fields (id) ON DELETE CASCADE,
    value_text   TEXT,
    value_number NUMERIC,
    value_date   DATE,
    value_bool   BOOLEAN,
    CONSTRAINT pk_issue_custom_values PRIMARY KEY (issue_id, field_id)
);

CREATE INDEX IF NOT EXISTS idx_issue_custom_values_issue
    ON issue_custom_values (issue_id);
