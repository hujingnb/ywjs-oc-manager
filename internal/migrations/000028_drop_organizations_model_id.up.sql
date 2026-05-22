-- 000028_drop_organizations_model_id.up.sql
-- 组织改为持有助手版本 allowlist，不再有单一默认模型；model_id 列已无业务意义。
ALTER TABLE organizations DROP COLUMN model_id;
