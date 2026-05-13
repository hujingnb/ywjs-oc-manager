-- 实例模型治理迁移。
-- 本地历史 app 数据均为测试数据，清理后避免为旧实例猜测错误模型。
DELETE FROM channel_bindings
WHERE app_id IN (SELECT id FROM apps);

DELETE FROM jobs
WHERE payload_json->>'app_id' IN (SELECT id::text FROM apps);

DELETE FROM audit_logs
WHERE target_type = 'app'
  AND target_id IN (SELECT id::text FROM apps);

DELETE FROM apps;

ALTER TABLE organizations
ADD COLUMN enabled_models jsonb NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE organizations
ADD CONSTRAINT organizations_enabled_models_array_check
CHECK (jsonb_typeof(enabled_models) = 'array');

COMMENT ON COLUMN organizations.enabled_models IS 'manager 层组织可用模型列表；new-api 不用该字段做权限控制。';

ALTER TABLE apps
ADD COLUMN model_id text NOT NULL;

ALTER TABLE apps
ADD CONSTRAINT apps_model_id_not_blank_check
CHECK (btrim(model_id) <> '');

COMMENT ON COLUMN apps.model_id IS '实例当前使用的模型 ID，由 manager 注入 OpenClaw 配置。';

CREATE INDEX apps_org_model_active_idx
ON apps(org_id, model_id)
WHERE deleted_at IS NULL;
