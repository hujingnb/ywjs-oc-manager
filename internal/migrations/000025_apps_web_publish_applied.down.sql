-- 回滚：移除 web_publish_applied 列。
ALTER TABLE apps DROP COLUMN web_publish_applied;
