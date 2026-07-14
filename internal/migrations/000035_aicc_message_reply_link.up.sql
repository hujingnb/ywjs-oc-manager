-- 助手回复显式关联触发它的访客消息，公开状态查询不能依赖可选 client_message_id。
ALTER TABLE aicc_messages
    ADD COLUMN reply_to_message_id CHAR(36) NULL AFTER client_message_id,
    ADD CONSTRAINT fk_aicc_messages_reply_to
        FOREIGN KEY (reply_to_message_id) REFERENCES aicc_messages(id) ON DELETE SET NULL,
    ADD KEY idx_aicc_messages_reply_to (reply_to_message_id, direction);
