-- 恢复 apps.persona_mode 和 apps.app_prompt 列（与原始 000002_core_schema.up.sql 定义一致）。
ALTER TABLE apps ADD COLUMN persona_mode text NOT NULL DEFAULT 'org_inherited';
ALTER TABLE apps ADD COLUMN app_prompt text NULL;

ALTER TABLE apps ADD CONSTRAINT apps_persona_mode_check CHECK (persona_mode IN ('org_inherited', 'app_override'));

COMMENT ON COLUMN apps.persona_mode IS '人设模式：继承组织人设或使用应用级覆盖提示词。';
