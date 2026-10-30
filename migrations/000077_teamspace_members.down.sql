-- 000077_teamspace_members down: drop the teamspace membership surface
-- (manual rollback only; the migrator never applies down files).
-- Dropping the table removes all membership rows; visibility reverts to
-- the cycle-89 shape (every workspace member sees every teamspace).
DROP INDEX IF EXISTS teamspace_members_user_idx;
DROP TABLE IF EXISTS teamspace_members;
