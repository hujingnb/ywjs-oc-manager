-- 客服消息任务是 Redis 通知之外的唯一调度事实来源；服务重启后可按状态与租约继续处理。
CREATE TABLE aicc_message_tasks (
    id CHAR(36) PRIMARY KEY,
    message_id CHAR(36) NOT NULL,
    session_id CHAR(36) NOT NULL,
    agent_id CHAR(36) NOT NULL,
    org_id CHAR(36) NOT NULL,
    app_id CHAR(36) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'queued',
    attempts INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 5,
    run_after DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    lease_token CHAR(36) NULL,
    lease_expires_at DATETIME(6) NULL,
    last_error TEXT NULL,
    -- processing_session_key 只在执行中保留 session；唯一键让并发 dispatcher 无法同时租约同一会话。
    processing_session_key CHAR(36) GENERATED ALWAYS AS (
        CASE WHEN status = 'processing' THEN session_id END
    ) STORED,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    CONSTRAINT aicc_message_tasks_status_check CHECK (status IN ('queued','processing','retry_wait','completed','failed')),
    CONSTRAINT aicc_message_tasks_attempts_check CHECK (attempts >= 0 AND max_attempts > 0),
    -- message_id 是任务唯一事实来源；消息已通过 aicc_messages 的会话外键级联删除。
    -- MySQL 在此表同时声明多组共享列的复合外键时会拒绝建表，故不重复声明会话、组织和应用链路。
    CONSTRAINT fk_aicc_message_tasks_message FOREIGN KEY (message_id) REFERENCES aicc_messages(id) ON DELETE CASCADE,
    UNIQUE KEY uk_aicc_message_tasks_message (message_id),
    UNIQUE KEY uk_aicc_message_tasks_processing_session (processing_session_key),
    KEY idx_aicc_message_tasks_ready (status, run_after, id),
    KEY idx_aicc_message_tasks_lease (status, lease_expires_at, id),
    KEY idx_aicc_message_tasks_session_status (session_id, status, id),
    KEY idx_aicc_message_tasks_agent_org (agent_id, org_id),
    KEY idx_aicc_message_tasks_app_org (app_id, org_id)
);
