-- conversation_files 记录 manager 自身的「对话文件上传」操作：文件本体存 S3，
-- manager 不持有对话消息本体，仅靠本表把 file_id 映射回 S3 对象以支持历史下载与重新签名。
CREATE TABLE conversation_files (
    id CHAR(36) PRIMARY KEY COMMENT '文件 ID（UUID），即消息 part 与 <oc-file:id> 标记里的 file_id',
    app_id CHAR(36) NOT NULL COMMENT '所属实例 ID',
    session_id VARCHAR(256) NOT NULL COMMENT '所属会话 ID（hermes session id，非 UUID）',
    s3_key VARCHAR(1024) NOT NULL COMMENT 'S3 对象键 apps/<appID>/conversations/<sid>/<fileID>/<filename>',
    filename VARCHAR(512) NOT NULL COMMENT '原始文件名（展示与下载用）',
    mime VARCHAR(255) NOT NULL DEFAULT '' COMMENT 'MIME 类型',
    size BIGINT NOT NULL DEFAULT 0 COMMENT '文件字节数',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '上传时间',
    KEY idx_conversation_files_app_session (app_id, session_id),
    CONSTRAINT fk_conversation_files_app FOREIGN KEY (app_id) REFERENCES apps(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
