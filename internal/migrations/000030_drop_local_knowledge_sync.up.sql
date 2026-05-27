-- RAGFlow 成为知识库唯一主库后，manager 不再向 runtime node 分发知识库文件。
-- 清理旧同步任务与状态表，避免新部署继续暴露不可用的本地同步状态模型。
DELETE FROM jobs WHERE type = 'knowledge_sync_node';

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
        'runtime_node_health_reconcile',
        'runtime_refresh_status',
        'app_health_check',
        'newapi_disable_key',
        'newapi_restore_key',
        'workspace_archive_cleanup'
    )
);

DROP TABLE IF EXISTS knowledge_sync_status;
