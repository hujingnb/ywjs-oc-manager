-- 回滚时先删除自引用外键和索引，再移除回复关联列。
ALTER TABLE aicc_messages
    DROP FOREIGN KEY fk_aicc_messages_reply_to,
    DROP KEY idx_aicc_messages_reply_to,
    DROP COLUMN reply_to_message_id;
