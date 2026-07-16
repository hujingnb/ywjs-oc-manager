-- AICC 运行时容器不保存会话状态；以下三张表是续聊摘要、回答依据与意向画像的唯一事实来源。
CREATE TABLE aicc_session_contexts (
    id CHAR(36) PRIMARY KEY,
    session_id CHAR(36) NOT NULL,
    summary TEXT NOT NULL,
    summarized_through_message_id CHAR(36) NULL,
    summary_version INT NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    CONSTRAINT aicc_session_contexts_version_check CHECK (summary_version >= 1),
    CONSTRAINT fk_aicc_session_contexts_session FOREIGN KEY (session_id) REFERENCES aicc_sessions(id) ON DELETE CASCADE,
    -- 消息 ID 虽全局唯一，仍带 session_id 组成复合外键，阻止错误把其他访客会话的摘要水位写入本会话。
    CONSTRAINT fk_aicc_session_contexts_message FOREIGN KEY (summarized_through_message_id, session_id)
        REFERENCES aicc_messages(id, session_id) ON DELETE CASCADE,
    UNIQUE KEY uk_aicc_session_contexts_session (session_id),
    KEY idx_aicc_session_contexts_message_session (summarized_through_message_id, session_id)
);

-- 每条助手回复可关联多个知识库或公开网络来源，unconfirmed 区分尚未获得企业确认的公开网络信息。
CREATE TABLE aicc_message_sources (
    id CHAR(36) PRIMARY KEY,
    message_id CHAR(36) NOT NULL,
    source_type VARCHAR(32) NOT NULL,
    title VARCHAR(512) NULL,
    url TEXT NULL,
    scope VARCHAR(32) NULL,
    reference_id VARCHAR(255) NULL,
    unconfirmed BOOLEAN NOT NULL DEFAULT FALSE,
    retrieved_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT aicc_message_sources_type_check CHECK (source_type IN ('knowledge','web')),
    CONSTRAINT fk_aicc_message_sources_message FOREIGN KEY (message_id) REFERENCES aicc_messages(id) ON DELETE CASCADE,
    KEY idx_aicc_message_sources_message_created (message_id, created_at, id),
    KEY idx_aicc_message_sources_reference (reference_id)
);

-- 每个会话仅保留一份可解释的意向画像；高意向而未形成正式线索的会话由本表直接派生匿名候选。
CREATE TABLE aicc_session_intents (
    id CHAR(36) PRIMARY KEY,
    session_id CHAR(36) NOT NULL,
    intent_level VARCHAR(16) NOT NULL DEFAULT 'low',
    fields_json JSON NULL,
    confidence_json JSON NULL,
    evidence_json JSON NULL,
    analyzer_version VARCHAR(128) NOT NULL,
    analyzed_message_id CHAR(36) NULL,
    invite_status VARCHAR(32) NOT NULL DEFAULT 'not_invited',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    CONSTRAINT aicc_session_intents_level_check CHECK (intent_level IN ('low','medium','high')),
    CONSTRAINT aicc_session_intents_invite_check CHECK (invite_status IN ('not_invited','invited','declined','submitted')),
    CONSTRAINT fk_aicc_session_intents_session FOREIGN KEY (session_id) REFERENCES aicc_sessions(id) ON DELETE CASCADE,
    -- 分析结果只能指向当前会话内的访客消息，不能跨会话借用证据。
    CONSTRAINT fk_aicc_session_intents_message FOREIGN KEY (analyzed_message_id, session_id)
        REFERENCES aicc_messages(id, session_id) ON DELETE CASCADE,
    UNIQUE KEY uk_aicc_session_intents_session (session_id),
    KEY idx_aicc_session_intents_level_created (intent_level, created_at, id),
    KEY idx_aicc_session_intents_message_session (analyzed_message_id, session_id)
);
