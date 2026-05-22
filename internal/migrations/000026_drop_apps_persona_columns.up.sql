-- 删除 apps 表的 persona_mode 和 app_prompt 遗留列。
-- 实例人设由绑定的助手版本 system_prompt 提供，这两列已无人读取。
ALTER TABLE apps DROP COLUMN persona_mode;
ALTER TABLE apps DROP COLUMN app_prompt;
