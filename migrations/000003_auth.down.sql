-- 000003_auth down: drop the auth tables (reverse FK order).
DROP TABLE IF EXISTS rate_limits;
DROP TABLE IF EXISTS otp_codes;
DROP TABLE IF EXISTS oauth_accounts;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;
