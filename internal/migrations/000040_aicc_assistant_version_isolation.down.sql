-- 旧版本无法消费 rollout 任务，必须在任何 DDL/DML 前清理，避免后续失败形成半回滚。
DELETE FROM jobs WHERE type = 'aicc_model_rollout';

-- 隔离期内助手版本可能被物理删除；回滚前把缺失映射安全降级为 NULL，避免恢复时触发 apps 外键。
-- 软删除版本仍物理存在并满足外键，必须保留原 ID 以忠实还原迁移前绑定。
UPDATE aicc_version_isolation_backups b
LEFT JOIN assistant_versions av ON av.id = b.version_id
SET b.version_id = NULL
WHERE b.version_id IS NOT NULL AND av.id IS NULL;

-- 先恢复组织旧列，保证配置回填期间旧代码仍能读取完整的 AICC 开关与上限。
ALTER TABLE organizations
    ADD COLUMN aicc_enabled BOOLEAN NOT NULL DEFAULT FALSE COMMENT '是否开通 AICC（AI Contact Center）能力',
    ADD COLUMN aicc_agent_limit INT NULL COMMENT 'AICC 智能体数量上限，NULL 表示不限',
    ADD CONSTRAINT organizations_aicc_agent_limit_check CHECK (aicc_agent_limit IS NULL OR aicc_agent_limit >= 0);

UPDATE organizations o
JOIN organization_aicc_configs c ON c.org_id = o.id
SET o.aicc_enabled = c.enabled,
    o.aicc_agent_limit = c.agent_limit;

-- 仅 AICC app 存在备份记录；直接按主键恢复其迁移前的助手版本映射。
UPDATE apps a
JOIN aicc_version_isolation_backups b ON b.app_id = a.id
SET a.version_id = b.version_id
WHERE a.app_type = 'aicc';

-- 回退 jobs CHECK 时仍保留 000023 引入的 web_publish_provision。
ALTER TABLE jobs
    DROP CONSTRAINT jobs_type_check,
    ADD CONSTRAINT jobs_type_check CHECK (type IN (
        'app_initialize','app_start_container','app_stop_container','app_restart_container','app_delete',
        'channel_start_login','channel_check_binding','runtime_node_health_reconcile','runtime_refresh_status',
        'app_health_check','newapi_disable_key','newapi_restore_key','workspace_archive_cleanup',
        'web_publish_provision'));

ALTER TABLE aicc_agents
    DROP COLUMN applied_config_revision,
    DROP COLUMN persona;

DROP TABLE aicc_version_isolation_backups;
DROP TABLE organization_aicc_configs;
