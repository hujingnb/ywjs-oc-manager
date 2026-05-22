-- 000028_drop_organizations_model_id.down.sql
-- 恢复 model_id 列，与 000022_org_single_model.up.sql 中的原始定义保持一致：text NOT NULL DEFAULT ''。
ALTER TABLE organizations ADD COLUMN model_id text NOT NULL DEFAULT '';
