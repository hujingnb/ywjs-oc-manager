-- 回滚时恢复旧本地知识库同步状态表与 knowledge_sync_node 任务类型。
CREATE TABLE knowledge_sync_status (
    org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    node_id         uuid NOT NULL REFERENCES runtime_nodes(id) ON DELETE CASCADE,
    status          text NOT NULL CHECK (status IN ('pending', 'synced', 'failed')),
    last_success_at timestamptz NULL,
    last_error      text NULL,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, node_id)
);

CREATE INDEX idx_knowledge_sync_status_org ON knowledge_sync_status(org_id);

ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_type_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_type_check CHECK (
    type IN (
        'app_initialize',
        'app_start_container',
        'app_stop_container',
        'app_restart_container',
        'app_delete',
        'channel_start_login',
        'channel_check_binding',
        'knowledge_sync_node',
        'runtime_node_health_reconcile',
        'runtime_refresh_status',
        'app_health_check',
        'newapi_disable_key',
        'newapi_restore_key',
        'workspace_archive_cleanup'
    )
);
