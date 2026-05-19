-- 000022_org_single_model.up.sql

-- 1. organizations: enabled_models → model_id
ALTER TABLE organizations DROP COLUMN IF EXISTS enabled_models;
ALTER TABLE organizations ADD COLUMN model_id text NOT NULL DEFAULT '';

-- 2. apps: 新增 model_synced 标记
ALTER TABLE apps ADD COLUMN model_synced boolean NOT NULL DEFAULT true;

-- 3. 移除旧索引（不再需要按 org+model 统计）
DROP INDEX IF EXISTS apps_org_model_active_idx;
