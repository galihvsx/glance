-- 000029_custom_fields down: drop the custom_fields table and its index.
DROP INDEX IF EXISTS idx_custom_fields_project;
DROP TABLE IF EXISTS custom_fields;
