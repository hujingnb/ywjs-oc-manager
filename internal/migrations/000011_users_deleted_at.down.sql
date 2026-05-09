DROP INDEX IF EXISTS users_active_idx;
ALTER TABLE users DROP COLUMN IF EXISTS deleted_at;
