DROP TABLE IF EXISTS aicc_feedback;
DROP TABLE IF EXISTS aicc_lead_values;
DROP TABLE IF EXISTS aicc_leads;
DROP TABLE IF EXISTS aicc_lead_fields;
DROP TABLE IF EXISTS aicc_messages;
DROP TABLE IF EXISTS aicc_sessions;
DROP TABLE IF EXISTS aicc_agent_knowledge;
DROP TABLE IF EXISTS aicc_agents;

ALTER TABLE ragflow_documents
    DROP INDEX uk_ragflow_documents_aicc_app_doc_identity;

ALTER TABLE apps
    DROP INDEX uk_apps_id_org,
    DROP COLUMN aicc_hidden;

ALTER TABLE organizations
    DROP CHECK organizations_aicc_agent_limit_check,
    DROP COLUMN aicc_agent_limit,
    DROP COLUMN aicc_enabled;
