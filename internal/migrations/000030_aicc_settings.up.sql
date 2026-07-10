CREATE TABLE aicc_agent_settings (
    agent_id CHAR(36) NOT NULL PRIMARY KEY,
    message_limit_per_session INT NOT NULL DEFAULT 100,
    sensitive_words_json JSON NULL,
    blocked_visitor_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    blocked_visitor_threshold_json JSON NULL,
    session_resume_ttl_minutes INT NOT NULL DEFAULT 30,
    analytics_config_json JSON NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    CONSTRAINT aicc_agent_settings_message_limit_check CHECK (message_limit_per_session BETWEEN 1 AND 1000),
    CONSTRAINT aicc_agent_settings_resume_ttl_check CHECK (session_resume_ttl_minutes BETWEEN 1 AND 1440),
    CONSTRAINT fk_aicc_agent_settings_agent FOREIGN KEY (agent_id) REFERENCES aicc_agents(id) ON DELETE CASCADE,
    UNIQUE KEY uk_aicc_agent_settings_agent (agent_id)
);

CREATE TABLE aicc_blocked_visitors (
    id CHAR(36) PRIMARY KEY,
    agent_id CHAR(36) NOT NULL,
    org_id CHAR(36) NOT NULL,
    visitor_hash VARCHAR(128) NOT NULL,
    reason VARCHAR(255) NOT NULL,
    expires_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    CONSTRAINT fk_aicc_blocked_visitors_agent_org FOREIGN KEY (agent_id, org_id) REFERENCES aicc_agents(id, org_id) ON DELETE CASCADE,
    CONSTRAINT fk_aicc_blocked_visitors_org FOREIGN KEY (org_id) REFERENCES organizations(id),
    UNIQUE KEY uk_aicc_blocked_visitors_agent_visitor (agent_id, visitor_hash),
    KEY idx_aicc_blocked_visitors_lookup (agent_id, visitor_hash, expires_at),
    KEY idx_aicc_blocked_visitors_agent_created (agent_id, created_at DESC, id DESC)
);
