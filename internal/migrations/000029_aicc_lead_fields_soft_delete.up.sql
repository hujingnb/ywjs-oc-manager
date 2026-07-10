ALTER TABLE aicc_lead_fields
    ADD COLUMN deleted_at DATETIME NULL COMMENT '字段停用时间；历史留资值仍通过 field_id 保留字段锚点',
    ADD KEY idx_aicc_lead_fields_agent_active (agent_id, deleted_at, sort_order, id);

ALTER TABLE aicc_lead_values
    DROP FOREIGN KEY fk_aicc_lead_values_session_agent;

ALTER TABLE aicc_lead_values
    ADD CONSTRAINT fk_aicc_lead_values_session_agent FOREIGN KEY (session_id, agent_id)
        REFERENCES aicc_sessions(id, agent_id) ON DELETE CASCADE;
