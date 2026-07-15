-- 意向分析属于主回复后的辅助能力：失败时独立重试，不能阻断或重复交付客服答复。
CREATE TABLE aicc_intent_analysis_retries (
    session_id CHAR(36) PRIMARY KEY,
    message_id CHAR(36) NOT NULL,
    attempts INT NOT NULL DEFAULT 0,
    run_after DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    last_error VARCHAR(512) NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    CONSTRAINT aicc_intent_analysis_retries_attempts_check CHECK (attempts >= 0),
    CONSTRAINT fk_aicc_intent_analysis_retries_session FOREIGN KEY (session_id) REFERENCES aicc_sessions(id) ON DELETE CASCADE,
    CONSTRAINT fk_aicc_intent_analysis_retries_message FOREIGN KEY (message_id, session_id) REFERENCES aicc_messages(id, session_id) ON DELETE CASCADE,
    KEY idx_aicc_intent_analysis_retries_ready (run_after, session_id)
);
