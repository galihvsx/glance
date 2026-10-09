-- 000027_favorites down: drop the favorites table and its index.
DROP INDEX IF EXISTS idx_favorites_user;
DROP TABLE IF EXISTS favorites;
