-- 000052_member_capacity: weekly capacity (story points) per workspace
-- member (cycle 29 "Workload & capacity").
--
-- The column is NULL = "no capacity set" (unset members never trip the
-- overloaded flag); a non-NULL value is the member's weekly budget in
-- estimate points, enforced 0..999 both here and in the service layer
-- (service.ErrInvalidCapacity). Idempotent: safe to re-run.
ALTER TABLE workspace_members
    ADD COLUMN IF NOT EXISTS weekly_capacity_points INTEGER NULL
    CHECK (weekly_capacity_points IS NULL
           OR (weekly_capacity_points >= 0 AND weekly_capacity_points <= 999));
