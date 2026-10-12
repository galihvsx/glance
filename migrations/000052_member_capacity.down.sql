-- C29T0 down: drop the member capacity column.
ALTER TABLE workspace_members
    DROP COLUMN IF EXISTS weekly_capacity_points;
