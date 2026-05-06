ALTER TABLE apps
    DROP COLUMN IF EXISTS health_state_json,
    DROP COLUMN IF EXISTS restart_policy_json;
