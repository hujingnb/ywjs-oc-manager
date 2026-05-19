-- 000022_org_single_model.down.sql

ALTER TABLE apps DROP COLUMN IF EXISTS model_synced;
ALTER TABLE organizations DROP COLUMN IF EXISTS model_id;
ALTER TABLE organizations ADD COLUMN enabled_models jsonb NOT NULL DEFAULT '[]';
CREATE INDEX IF NOT EXISTS apps_org_model_active_idx ON apps(org_id, model_id) WHERE deleted_at IS NULL;
