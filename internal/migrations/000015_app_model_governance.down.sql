DROP INDEX IF EXISTS apps_org_model_active_idx;

ALTER TABLE apps
DROP CONSTRAINT IF EXISTS apps_model_id_not_blank_check;

ALTER TABLE apps
DROP COLUMN IF EXISTS model_id;

ALTER TABLE organizations
DROP CONSTRAINT IF EXISTS organizations_enabled_models_array_check;

ALTER TABLE organizations
DROP COLUMN IF EXISTS enabled_models;
