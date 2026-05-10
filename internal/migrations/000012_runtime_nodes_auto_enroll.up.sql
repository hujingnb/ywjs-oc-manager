-- runtime agent 自动注册：节点由 agent enroll 自动创建，后台不再发放 bootstrap token。
ALTER TABLE runtime_nodes
    ADD COLUMN agent_id text NULL,
    ADD COLUMN last_probe_attempted_at timestamptz NULL,
    ADD COLUMN last_probe_ok_at timestamptz NULL,
    ADD COLUMN last_probe_failed_at timestamptz NULL,
    ADD COLUMN last_probe_error text NULL,
    ADD COLUMN probe_failure_streak integer NOT NULL DEFAULT 0,
    ADD COLUMN probe_success_streak integer NOT NULL DEFAULT 0;

CREATE UNIQUE INDEX runtime_nodes_agent_id_uniq
    ON runtime_nodes(agent_id)
    WHERE agent_id IS NOT NULL;

ALTER TABLE runtime_nodes DROP CONSTRAINT runtime_nodes_name_key;
ALTER TABLE runtime_nodes DROP COLUMN bootstrap_token_hash;
ALTER TABLE runtime_nodes DROP COLUMN bootstrap_token_expires_at;

ALTER TABLE runtime_nodes DROP CONSTRAINT runtime_nodes_status_check;
ALTER TABLE runtime_nodes
    ADD CONSTRAINT runtime_nodes_status_check
    CHECK (status IN ('pending', 'active', 'unreachable', 'disabled', 'degraded'));

COMMENT ON COLUMN runtime_nodes.agent_id IS
    'agent 自身持久化的 UUID（state_dir/agent-id），全表唯一；enroll 幂等键。';
COMMENT ON COLUMN runtime_nodes.last_probe_attempted_at IS
    'manager 最近一次主动探测该 agent 双端口的发起时间；无论请求是否到达 agent 都更新。';
COMMENT ON COLUMN runtime_nodes.last_probe_ok_at IS
    'manager 最近一次对 agent 双端口探测全部通过的时间。';
COMMENT ON COLUMN runtime_nodes.last_probe_failed_at IS
    'manager 最近一次对 agent 双端口探测任一失败的时间。';
COMMENT ON COLUMN runtime_nodes.last_probe_error IS
    'manager 最近一次 probe 失败的简短错误分类。';
COMMENT ON COLUMN runtime_nodes.probe_failure_streak IS
    'manager 主动探测连续失败次数，用于 active 到 degraded 的阈值判断。';
COMMENT ON COLUMN runtime_nodes.probe_success_streak IS
    'manager 主动探测连续成功次数，用于 degraded 到 active 的阈值判断。';
