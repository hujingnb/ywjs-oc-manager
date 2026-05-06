ALTER TABLE apps
    DROP COLUMN IF EXISTS runtime_snapshot_at,
    DROP COLUMN IF EXISTS runtime_snapshot_json;
