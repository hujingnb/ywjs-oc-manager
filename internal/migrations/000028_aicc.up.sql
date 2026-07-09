ALTER TABLE organizations
    ADD COLUMN aicc_enabled BOOLEAN NOT NULL DEFAULT FALSE COMMENT '是否开通 AICC（AI Contact Center）能力',
    ADD COLUMN aicc_agent_limit INT NULL COMMENT 'AICC 智能体数量上限，NULL 表示不限';

ALTER TABLE apps
    ADD COLUMN aicc_hidden BOOLEAN NOT NULL DEFAULT FALSE COMMENT '是否为 AICC 自动创建的隐藏 app';

CREATE TABLE aicc_agents (
    id CHAR(36) PRIMARY KEY,
    org_id CHAR(36) NOT NULL,
    app_id CHAR(36) NOT NULL,
    name VARCHAR(128) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'draft',
    scenario TEXT NULL,
    greeting TEXT NULL,
    answer_boundary TEXT NULL,
    privacy_mode VARCHAR(32) NOT NULL DEFAULT 'notice',
    privacy_text TEXT NULL,
    retention_days INT NOT NULL DEFAULT 180,
    theme_json JSON NULL,
    allowed_domains_json JSON NULL,
    public_token VARCHAR(96) NOT NULL,
    widget_token VARCHAR(96) NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at DATETIME NULL,
    CONSTRAINT aicc_agents_status_check CHECK (status IN ('draft','active','paused','deleted')),
    CONSTRAINT aicc_agents_privacy_mode_check CHECK (privacy_mode IN ('notice','consent_required')),
    CONSTRAINT aicc_agents_retention_days_check CHECK (retention_days BETWEEN 1 AND 3650),
    CONSTRAINT fk_aicc_agents_org_id FOREIGN KEY (org_id) REFERENCES organizations(id),
    CONSTRAINT fk_aicc_agents_app_id FOREIGN KEY (app_id) REFERENCES apps(id),
    UNIQUE KEY uk_aicc_agents_public_token (public_token),
    UNIQUE KEY uk_aicc_agents_widget_token (widget_token),
    KEY idx_aicc_agents_org_status (org_id, status, deleted_at)
);

CREATE TABLE aicc_agent_knowledge (
    id CHAR(36) PRIMARY KEY,
    agent_id CHAR(36) NOT NULL,
    scope_type VARCHAR(32) NOT NULL,
    scope_id CHAR(36) NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT aicc_agent_knowledge_scope_type_check CHECK (scope_type IN ('org','industry','app_document')),
    CONSTRAINT fk_aicc_agent_knowledge_agent FOREIGN KEY (agent_id) REFERENCES aicc_agents(id) ON DELETE CASCADE,
    UNIQUE KEY uk_aicc_agent_knowledge_scope (agent_id, scope_type, scope_id)
);

CREATE TABLE aicc_sessions (
    id CHAR(36) PRIMARY KEY,
    agent_id CHAR(36) NOT NULL,
    org_id CHAR(36) NOT NULL,
    session_token VARCHAR(128) NOT NULL,
    channel VARCHAR(32) NOT NULL DEFAULT 'web_link',
    source_url TEXT NULL,
    referrer TEXT NULL,
    region VARCHAR(128) NULL,
    ip_hash VARCHAR(128) NULL,
    user_agent_hash VARCHAR(128) NULL,
    privacy_notice_shown BOOLEAN NOT NULL DEFAULT FALSE,
    privacy_consented_at DATETIME NULL,
    resolution_status VARCHAR(32) NOT NULL DEFAULT 'unknown',
    lead_status VARCHAR(32) NOT NULL DEFAULT 'pending',
    last_active_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    CONSTRAINT aicc_sessions_channel_check CHECK (channel IN ('web_link','web_widget','voice')),
    CONSTRAINT aicc_sessions_resolution_check CHECK (resolution_status IN ('resolved','unresolved','unknown')),
    CONSTRAINT aicc_sessions_lead_status_check CHECK (lead_status IN ('pending','complete','skipped')),
    CONSTRAINT fk_aicc_sessions_agent FOREIGN KEY (agent_id) REFERENCES aicc_agents(id),
    CONSTRAINT fk_aicc_sessions_org FOREIGN KEY (org_id) REFERENCES organizations(id),
    UNIQUE KEY uk_aicc_sessions_token (session_token),
    KEY idx_aicc_sessions_agent_time (agent_id, created_at DESC),
    KEY idx_aicc_sessions_retention (expires_at, agent_id)
);

CREATE TABLE aicc_messages (
    id CHAR(36) PRIMARY KEY,
    session_id CHAR(36) NOT NULL,
    agent_id CHAR(36) NOT NULL,
    direction VARCHAR(16) NOT NULL,
    content_type VARCHAR(32) NOT NULL DEFAULT 'text',
    text_content TEXT NULL,
    image_object_key VARCHAR(1024) NULL,
    image_mime VARCHAR(128) NULL,
    image_size_bytes BIGINT NULL,
    hermes_message_id VARCHAR(255) NULL,
    is_fallback BOOLEAN NOT NULL DEFAULT FALSE,
    is_refusal BOOLEAN NOT NULL DEFAULT FALSE,
    error_summary TEXT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT aicc_messages_direction_check CHECK (direction IN ('visitor','assistant','system')),
    CONSTRAINT aicc_messages_content_type_check CHECK (content_type IN ('text','image','mixed')),
    CONSTRAINT fk_aicc_messages_session FOREIGN KEY (session_id) REFERENCES aicc_sessions(id) ON DELETE CASCADE,
    CONSTRAINT fk_aicc_messages_agent FOREIGN KEY (agent_id) REFERENCES aicc_agents(id),
    KEY idx_aicc_messages_session_time (session_id, created_at, id)
);

CREATE TABLE aicc_lead_fields (
    id CHAR(36) PRIMARY KEY,
    agent_id CHAR(36) NOT NULL,
    field_key VARCHAR(64) NOT NULL,
    label VARCHAR(128) NOT NULL,
    field_type VARCHAR(32) NOT NULL DEFAULT 'text',
    required BOOLEAN NOT NULL DEFAULT FALSE,
    prompt_text TEXT NULL,
    sort_order INT NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    CONSTRAINT aicc_lead_fields_type_check CHECK (field_type IN ('text','phone','email','number')),
    CONSTRAINT fk_aicc_lead_fields_agent FOREIGN KEY (agent_id) REFERENCES aicc_agents(id) ON DELETE CASCADE,
    UNIQUE KEY uk_aicc_lead_fields_key (agent_id, field_key)
);

CREATE TABLE aicc_leads (
    id CHAR(36) PRIMARY KEY,
    org_id CHAR(36) NOT NULL,
    primary_contact_hash VARCHAR(128) NOT NULL,
    display_name VARCHAR(255) NULL,
    unread BOOLEAN NOT NULL DEFAULT TRUE,
    latest_session_id CHAR(36) NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    CONSTRAINT fk_aicc_leads_org FOREIGN KEY (org_id) REFERENCES organizations(id),
    UNIQUE KEY uk_aicc_leads_contact (org_id, primary_contact_hash),
    KEY idx_aicc_leads_org_unread (org_id, unread, updated_at DESC)
);

CREATE TABLE aicc_lead_values (
    id CHAR(36) PRIMARY KEY,
    session_id CHAR(36) NOT NULL,
    lead_id CHAR(36) NULL,
    field_id CHAR(36) NOT NULL,
    value_text TEXT NOT NULL,
    value_hash VARCHAR(128) NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_aicc_lead_values_session FOREIGN KEY (session_id) REFERENCES aicc_sessions(id) ON DELETE CASCADE,
    CONSTRAINT fk_aicc_lead_values_lead FOREIGN KEY (lead_id) REFERENCES aicc_leads(id) ON DELETE SET NULL,
    CONSTRAINT fk_aicc_lead_values_field FOREIGN KEY (field_id) REFERENCES aicc_lead_fields(id),
    KEY idx_aicc_lead_values_session (session_id)
);

CREATE TABLE aicc_feedback (
    id CHAR(36) PRIMARY KEY,
    session_id CHAR(36) NOT NULL,
    message_id CHAR(36) NOT NULL,
    helpful BOOLEAN NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_aicc_feedback_session FOREIGN KEY (session_id) REFERENCES aicc_sessions(id) ON DELETE CASCADE,
    CONSTRAINT fk_aicc_feedback_message FOREIGN KEY (message_id) REFERENCES aicc_messages(id) ON DELETE CASCADE,
    UNIQUE KEY uk_aicc_feedback_message (message_id)
);
