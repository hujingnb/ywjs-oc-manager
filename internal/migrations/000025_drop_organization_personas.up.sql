-- 000025_drop_organization_personas.up.sql
-- 组织 AI 人设特性已被助手版本内置提示词（version.SystemPrompt）取代。
-- 删除 organization_personas 表及其索引；hermes 内置提示词机制不受影响。
DROP TABLE IF EXISTS organization_personas;
