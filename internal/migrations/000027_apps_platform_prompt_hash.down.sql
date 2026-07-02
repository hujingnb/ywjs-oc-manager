-- 回滚：移除 applied_platform_prompt_hash 列。
ALTER TABLE apps DROP COLUMN applied_platform_prompt_hash;
