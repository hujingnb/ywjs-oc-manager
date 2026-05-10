-- 回滚自动注册 schema：恢复手工 bootstrap 注册所需字段和旧状态约束。
ALTER TABLE runtime_nodes DROP CONSTRAINT runtime_nodes_status_check;
ALTER TABLE runtime_nodes
    ADD CONSTRAINT runtime_nodes_status_check
    CHECK (status IN ('pending', 'active', 'unreachable', 'disabled'));

ALTER TABLE runtime_nodes
    ADD COLUMN bootstrap_token_hash text NULL,
    ADD COLUMN bootstrap_token_expires_at timestamptz NULL;

ALTER TABLE runtime_nodes ADD CONSTRAINT runtime_nodes_name_key UNIQUE (name);

DROP INDEX IF EXISTS runtime_nodes_agent_id_uniq;

ALTER TABLE runtime_nodes
    DROP COLUMN probe_success_streak,
    DROP COLUMN probe_failure_streak,
    DROP COLUMN last_probe_error,
    DROP COLUMN last_probe_failed_at,
    DROP COLUMN last_probe_ok_at,
    DROP COLUMN last_probe_attempted_at,
    DROP COLUMN agent_id;
