-- 回滚：恢复 NOT NULL（存在 NULL 行时回滚会失败，需先清理）。
ALTER TABLE apps MODIFY COLUMN runtime_node_id CHAR(36) NOT NULL;
