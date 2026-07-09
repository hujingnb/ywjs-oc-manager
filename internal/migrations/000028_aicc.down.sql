DROP TABLE IF EXISTS aicc_feedback;
DROP TABLE IF EXISTS aicc_lead_values;
DROP TABLE IF EXISTS aicc_leads;
DROP TABLE IF EXISTS aicc_lead_fields;
DROP TABLE IF EXISTS aicc_messages;
DROP TABLE IF EXISTS aicc_sessions;
DROP TABLE IF EXISTS aicc_agent_knowledge;
DROP TABLE IF EXISTS aicc_agents;

ALTER TABLE apps
    DROP COLUMN aicc_hidden;

ALTER TABLE organizations
    DROP COLUMN aicc_agent_limit,
    DROP COLUMN aicc_enabled;
