-- 000017_module_dates down: drop what the up migration added.
ALTER TABLE modules DROP CONSTRAINT IF EXISTS modules_dates_check;
ALTER TABLE modules DROP COLUMN IF EXISTS target_date;
ALTER TABLE modules DROP COLUMN IF EXISTS start_date;
