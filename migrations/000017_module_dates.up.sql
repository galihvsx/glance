-- 000017_module_dates: modules gain optional start/target dates (Plane
-- parity; C3T1 ships the modules API).
--
-- The modules / module_issues / module_members tables already exist from
-- 000012 (shipped tables-only, no API); this migration only adds the two
-- date columns. Issue membership uses the module_issues junction table
-- (like cycle_issues), NOT an issues.module_id column.
--
-- Everything here is idempotent (IF NOT EXISTS / guarded DO block) like
-- the rest of the chain.
ALTER TABLE modules ADD COLUMN IF NOT EXISTS start_date DATE;
ALTER TABLE modules ADD COLUMN IF NOT EXISTS target_date DATE;
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'modules_dates_check'
    ) THEN
        ALTER TABLE modules ADD CONSTRAINT modules_dates_check
            CHECK (start_date IS NULL OR target_date IS NULL OR start_date <= target_date);
    END IF;
END $$;
