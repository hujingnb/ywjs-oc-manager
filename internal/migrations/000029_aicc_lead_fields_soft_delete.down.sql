ALTER TABLE aicc_lead_values
    DROP FOREIGN KEY fk_aicc_lead_values_session_agent;

ALTER TABLE aicc_lead_values
    ADD CONSTRAINT fk_aicc_lead_values_session_agent FOREIGN KEY (session_id, agent_id)
        REFERENCES aicc_sessions(id, agent_id);

ALTER TABLE aicc_lead_fields
    DROP INDEX idx_aicc_lead_fields_agent_active,
    DROP COLUMN deleted_at;
