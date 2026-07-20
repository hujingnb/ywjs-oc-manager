-- 上线前必须执行以下只读预检；若返回企业 ID，需先补齐有效首版本，避免启用企业迁出后缺少运行模型：
-- SELECT o.id FROM organizations o
-- LEFT JOIN assistant_versions av
--   ON av.id = JSON_UNQUOTE(JSON_EXTRACT(o.assistant_version_ids, '$[0]'))
--  AND av.deleted_at IS NULL
-- WHERE o.aicc_enabled = TRUE AND av.id IS NULL;

-- 临时 guard 不产生永久 schema；若预检遗漏，CHECK 会在任何永久 DDL 前中止迁移。
CREATE TEMPORARY TABLE aicc_version_isolation_guard (
    enabled BOOLEAN NOT NULL,
    model VARCHAR(191) NULL,
    CONSTRAINT aicc_version_isolation_guard_enabled_model_check CHECK (
        enabled = FALSE OR (model IS NOT NULL AND LENGTH(TRIM(model)) > 0)
    )
);

INSERT INTO aicc_version_isolation_guard (enabled, model)
SELECT o.aicc_enabled, av.main_model
FROM organizations o
LEFT JOIN assistant_versions av
  ON av.id = JSON_UNQUOTE(JSON_EXTRACT(o.assistant_version_ids, '$[0]'))
 AND av.deleted_at IS NULL;

DROP TEMPORARY TABLE aicc_version_isolation_guard;

-- AICC 配置独立于组织主表持久化，revision 用于异步 rollout 判断智能体是否已应用最新模型。
CREATE TABLE organization_aicc_configs (
    org_id CHAR(36) PRIMARY KEY,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    model VARCHAR(191) NULL,
    agent_limit INT NULL,
    revision INT NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    CONSTRAINT fk_organization_aicc_configs_org FOREIGN KEY (org_id) REFERENCES organizations(id) ON DELETE CASCADE,
    CONSTRAINT organization_aicc_configs_limit_check CHECK (agent_limit IS NULL OR agent_limit >= 0),
    CONSTRAINT organization_aicc_configs_revision_check CHECK (revision > 0),
    CONSTRAINT organization_aicc_configs_enabled_model_check CHECK (
        enabled = FALSE OR (model IS NOT NULL AND LENGTH(TRIM(model)) > 0)
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 备份表故意不引用 assistant_versions，确保解除绑定后旧版本生命周期不再阻塞 AICC 数据演进。
CREATE TABLE aicc_version_isolation_backups (
    app_id CHAR(36) PRIMARY KEY,
    version_id CHAR(36) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- persona 保存智能体独立人设；applied_config_revision 从零开始，等待 rollout 应用企业配置。
ALTER TABLE aicc_agents
    ADD COLUMN persona TEXT NULL AFTER name,
    ADD COLUMN applied_config_revision INT NOT NULL DEFAULT 0 AFTER persona;

-- 回滚映射必须在清空 version_id 前完整保存，包括原本未绑定版本的 AICC app。
INSERT INTO aicc_version_isolation_backups (app_id, version_id)
SELECT id, version_id
FROM apps
WHERE app_type = 'aicc';

-- enabled 企业若没有有效首版本或模型为空，会触发 enabled/model CHECK 并中止迁移，防止静默失配。
INSERT INTO organization_aicc_configs (org_id, enabled, model, agent_limit, revision)
SELECT o.id, o.aicc_enabled, av.main_model, o.aicc_agent_limit, 1
FROM organizations o
LEFT JOIN assistant_versions av
  ON av.id = JSON_UNQUOTE(JSON_EXTRACT(o.assistant_version_ids, '$[0]'))
 AND av.deleted_at IS NULL;

UPDATE apps SET version_id = NULL WHERE app_type = 'aicc';

-- 回填成功后删除旧配置来源，避免同一业务状态被两套字段同时写入。
ALTER TABLE organizations
    DROP CHECK organizations_aicc_agent_limit_check,
    DROP COLUMN aicc_agent_limit,
    DROP COLUMN aicc_enabled;

-- 保留全部既有 job type，仅增加企业模型变更后的智能体 rollout 类型。
ALTER TABLE jobs
    DROP CONSTRAINT jobs_type_check,
    ADD CONSTRAINT jobs_type_check CHECK (type IN (
        'app_initialize','app_start_container','app_stop_container','app_restart_container','app_delete',
        'channel_start_login','channel_check_binding','runtime_node_health_reconcile','runtime_refresh_status',
        'app_health_check','newapi_disable_key','newapi_restore_key','workspace_archive_cleanup',
        'web_publish_provision','aicc_model_rollout'));
