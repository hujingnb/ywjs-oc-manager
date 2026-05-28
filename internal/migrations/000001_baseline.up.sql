-- OC-Manager MySQL 8 基线 schema（由 30 个 PG 增量迁移合并而成）
-- 字符集 utf8mb4 / utf8mb4_0900_ai_ci，引擎 InnoDB。
SET FOREIGN_KEY_CHECKS = 0;

-- 组织租户表
CREATE TABLE organizations (
    id CHAR(36) PRIMARY KEY COMMENT '组织 ID',
    name VARCHAR(255) NOT NULL UNIQUE COMMENT '组织名称',
    status VARCHAR(50) NOT NULL DEFAULT 'active' COMMENT '组织状态',
    contact_name VARCHAR(255) NULL,
    contact_phone VARCHAR(50) NULL,
    remark TEXT NULL,
    newapi_user_id VARCHAR(255) NULL,
    credit_warning_threshold INT NULL,
    newapi_user_credentials_ciphertext TEXT NULL,
    code VARCHAR(32) NOT NULL UNIQUE COMMENT '组织代码，登录命名空间',
    newapi_username VARCHAR(255) NULL,
    assistant_version_ids JSON NOT NULL DEFAULT (JSON_ARRAY()) COMMENT '可用助手版本 ID allowlist',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    CONSTRAINT organizations_status_check CHECK (status IN ('active','disabled','deleted')),
    CONSTRAINT organizations_credit_warning_threshold_check CHECK (
        credit_warning_threshold IS NULL OR (credit_warning_threshold >= 0 AND credit_warning_threshold <= 100)),
    CONSTRAINT organizations_code_format_check CHECK (code REGEXP '^[a-z0-9]([a-z0-9-]{1,30}[a-z0-9])$'),
    KEY idx_organizations_status_name (status, name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 用户表（platform_username_key 生成列替代 PG 部分唯一索引 WHERE org_id IS NULL）
CREATE TABLE users (
    id CHAR(36) PRIMARY KEY,
    org_id CHAR(36) NULL COMMENT '所属组织（平台管理员为空）',
    username VARCHAR(255) NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    display_name VARCHAR(255) NOT NULL,
    role VARCHAR(50) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    last_login_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL COMMENT '下线时间',
    platform_username_key VARCHAR(255) GENERATED ALWAYS AS (CASE WHEN org_id IS NULL THEN username END) VIRTUAL,
    CONSTRAINT users_role_check CHECK (role IN ('platform_admin','org_admin','org_member')),
    CONSTRAINT users_status_check CHECK (status IN ('active','disabled')),
    CONSTRAINT users_platform_org_check CHECK (
        (role = 'platform_admin' AND org_id IS NULL)
        OR (role IN ('org_admin','org_member') AND org_id IS NOT NULL)),
    CONSTRAINT fk_users_org_id FOREIGN KEY (org_id) REFERENCES organizations(id),
    UNIQUE KEY uk_users_org_username (org_id, username),
    UNIQUE KEY uk_users_platform_username (platform_username_key),
    KEY idx_users_org_role_status (org_id, role, status),
    KEY idx_users_active (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 运行节点表（spec-C 保留；workstream A 删除）
CREATE TABLE runtime_nodes (
    id CHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    agent_docker_endpoint VARCHAR(500) NULL,
    agent_file_endpoint VARCHAR(500) NULL,
    agent_tls_ca_cert TEXT NULL,
    agent_token_hash VARCHAR(255) NULL,
    agent_token_ciphertext TEXT NULL,
    agent_version VARCHAR(100) NULL,
    heartbeat_interval_seconds INT NOT NULL DEFAULT 30,
    last_heartbeat_at DATETIME(6) NULL,
    resource_snapshot_json JSON NULL,
    metadata_json JSON NULL,
    node_data_root VARCHAR(500) NULL,
    registered_at DATETIME(6) NULL,
    max_apps INT NULL,
    agent_id VARCHAR(255) NULL UNIQUE,
    last_probe_attempted_at DATETIME(6) NULL,
    last_probe_ok_at DATETIME(6) NULL,
    last_probe_failed_at DATETIME(6) NULL,
    last_probe_error VARCHAR(255) NULL,
    probe_failure_streak INT NOT NULL DEFAULT 0,
    probe_success_streak INT NOT NULL DEFAULT 0,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    CONSTRAINT runtime_nodes_status_check CHECK (status IN ('pending','active','unreachable','disabled','degraded')),
    CONSTRAINT runtime_nodes_heartbeat_interval_check CHECK (heartbeat_interval_seconds > 0),
    KEY idx_runtime_nodes_status_last_heartbeat (status, last_heartbeat_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 助手版本表（apps 外键引用，故先于 apps 建）
CREATE TABLE assistant_versions (
    id CHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT NOT NULL DEFAULT (''),
    system_prompt TEXT NOT NULL,
    image_id VARCHAR(255) NOT NULL,
    main_model VARCHAR(255) NOT NULL,
    routing_json JSON NOT NULL DEFAULT (JSON_OBJECT()),
    skills_json JSON NOT NULL DEFAULT (JSON_ARRAY()),
    revision INT NOT NULL DEFAULT 1,
    created_by CHAR(36) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    name_active_key VARCHAR(255) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN name END) STORED,
    CONSTRAINT assistant_versions_revision_check CHECK (revision > 0),
    CONSTRAINT fk_assistant_versions_created_by FOREIGN KEY (created_by) REFERENCES users(id),
    UNIQUE KEY uk_assistant_versions_name_active (name_active_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 应用表（owner_active_key / runtime_token_active_key 生成列替代部分唯一索引）
CREATE TABLE apps (
    id CHAR(36) PRIMARY KEY,
    org_id CHAR(36) NOT NULL,
    owner_user_id CHAR(36) NOT NULL,
    runtime_node_id CHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'draft',
    container_id VARCHAR(255) NULL,
    container_name VARCHAR(255) NULL,
    newapi_key_id VARCHAR(255) NULL,
    newapi_key_ciphertext TEXT NULL,
    api_key_status VARCHAR(50) NOT NULL DEFAULT 'pending',
    runtime_snapshot_json JSON NULL,
    runtime_snapshot_at DATETIME(6) NULL,
    restart_policy_json JSON NOT NULL
        DEFAULT (CAST('{"mode":"on_failure","max_per_window":5,"window_seconds":600}' AS JSON)),
    health_state_json JSON NULL,
    progress_current BIGINT NULL,
    progress_total BIGINT NULL,
    last_error_status VARCHAR(50) NULL,
    last_error_message TEXT NULL,
    runtime_image_ref VARCHAR(500) NOT NULL DEFAULT '',
    runtime_image_sha256 VARCHAR(255) NOT NULL DEFAULT '',
    newapi_key_name VARCHAR(255) NULL,
    version_id CHAR(36) NULL,
    applied_version_revision INT NOT NULL DEFAULT 0,
    applied_image_ref VARCHAR(500) NOT NULL DEFAULT '',
    runtime_token_hash VARCHAR(255) NULL,
    runtime_token_ciphertext TEXT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    owner_active_key CHAR(36) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN owner_user_id END) VIRTUAL,
    runtime_token_active_key VARCHAR(255) GENERATED ALWAYS AS (
        CASE WHEN runtime_token_hash IS NOT NULL AND deleted_at IS NULL THEN runtime_token_hash END) VIRTUAL,
    CONSTRAINT apps_status_check CHECK (status IN (
        'draft','pulling_runtime_image','pulling_image','syncing_image','preparing_runtime',
        'creating_container','starting','binding_waiting','binding_failed',
        'running','stopped','error','deleted')),
    CONSTRAINT apps_api_key_status_check CHECK (api_key_status IN ('pending','active','disabled','error')),
    CONSTRAINT apps_runtime_token_pair_check CHECK (
        (runtime_token_hash IS NULL AND runtime_token_ciphertext IS NULL)
        OR (runtime_token_hash IS NOT NULL AND runtime_token_ciphertext IS NOT NULL)),
    CONSTRAINT fk_apps_org_id FOREIGN KEY (org_id) REFERENCES organizations(id),
    CONSTRAINT fk_apps_owner_user_id FOREIGN KEY (owner_user_id) REFERENCES users(id),
    CONSTRAINT fk_apps_runtime_node_id FOREIGN KEY (runtime_node_id) REFERENCES runtime_nodes(id),
    CONSTRAINT fk_apps_version_id FOREIGN KEY (version_id) REFERENCES assistant_versions(id),
    UNIQUE KEY uk_apps_owner_active (owner_active_key),
    UNIQUE KEY uk_apps_runtime_token_hash_active (runtime_token_active_key),
    KEY idx_apps_org_active_created (org_id, deleted_at, created_at DESC),
    KEY idx_apps_runtime_node_status (runtime_node_id, status),
    KEY idx_apps_newapi_key_id (newapi_key_id),
    KEY idx_apps_version_id (version_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 渠道绑定表（app_active_key 生成列替代 WHERE status<>'deleted' 唯一索引）
CREATE TABLE channel_bindings (
    id CHAR(36) PRIMARY KEY,
    app_id CHAR(36) NOT NULL,
    channel_type VARCHAR(50) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'unbound',
    bound_identity VARCHAR(255) NULL,
    channel_name VARCHAR(255) NULL,
    metadata_json JSON NULL,
    bound_at DATETIME(6) NULL,
    last_online_at DATETIME(6) NULL,
    last_error TEXT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    app_active_key CHAR(36) GENERATED ALWAYS AS (CASE WHEN status <> 'deleted' THEN app_id END) VIRTUAL,
    CONSTRAINT channel_bindings_status_check CHECK (status IN (
        'unbound','pending_auth','bound','failed','expired','unbound_by_user','deleted')),
    CONSTRAINT channel_bindings_channel_type_check CHECK (channel_type IN ('wechat')),
    CONSTRAINT fk_channel_bindings_app_id FOREIGN KEY (app_id) REFERENCES apps(id),
    UNIQUE KEY uk_channel_bindings_app_active (app_active_key),
    KEY idx_channel_bindings_app_channel_status (app_id, channel_type, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 充值记录表
CREATE TABLE recharge_records (
    id CHAR(36) PRIMARY KEY,
    org_id CHAR(36) NOT NULL,
    operator_id CHAR(36) NOT NULL,
    credit_amount BIGINT NOT NULL,
    remark TEXT NULL,
    newapi_ref_id VARCHAR(255) NULL,
    status VARCHAR(50) NOT NULL,
    error_message TEXT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT recharge_records_credit_amount_check CHECK (credit_amount > 0),
    CONSTRAINT recharge_records_status_check CHECK (status IN ('succeeded','failed')),
    CONSTRAINT fk_recharge_records_org_id FOREIGN KEY (org_id) REFERENCES organizations(id),
    CONSTRAINT fk_recharge_records_operator_id FOREIGN KEY (operator_id) REFERENCES users(id),
    KEY idx_recharge_records_org_created (org_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 异步任务表
CREATE TABLE jobs (
    id CHAR(36) PRIMARY KEY,
    type VARCHAR(100) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    priority INT NOT NULL DEFAULT 0,
    run_after DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    attempts INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 5,
    payload_json JSON NOT NULL DEFAULT (JSON_OBJECT()),
    locked_by VARCHAR(255) NULL,
    locked_at DATETIME(6) NULL,
    last_error TEXT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    finished_at DATETIME(6) NULL,
    CONSTRAINT jobs_status_check CHECK (status IN ('pending','running','succeeded','failed','canceled')),
    CONSTRAINT jobs_attempts_check CHECK (attempts >= 0),
    CONSTRAINT jobs_max_attempts_check CHECK (max_attempts > 0),
    CONSTRAINT jobs_type_check CHECK (type IN (
        'app_initialize','app_start_container','app_stop_container','app_restart_container','app_delete',
        'channel_start_login','channel_check_binding','runtime_node_health_reconcile','runtime_refresh_status',
        'app_health_check','newapi_disable_key','newapi_restore_key','workspace_archive_cleanup')),
    KEY idx_jobs_status_run_after_priority (status, run_after, priority)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 审计日志表
CREATE TABLE audit_logs (
    id CHAR(36) PRIMARY KEY,
    actor_id CHAR(36) NULL,
    actor_role VARCHAR(50) NOT NULL,
    org_id CHAR(36) NULL,
    target_type VARCHAR(100) NOT NULL,
    target_id VARCHAR(255) NOT NULL,
    action VARCHAR(100) NOT NULL,
    result VARCHAR(50) NOT NULL,
    error_message TEXT NULL,
    ip_address VARCHAR(45) NULL,
    metadata_json JSON NULL,
    detail_message TEXT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT audit_logs_actor_role_check CHECK (actor_role IN ('system','platform_admin','org_admin','org_member')),
    CONSTRAINT audit_logs_result_check CHECK (result IN ('succeeded','failed')),
    CONSTRAINT fk_audit_logs_actor_id FOREIGN KEY (actor_id) REFERENCES users(id),
    CONSTRAINT fk_audit_logs_org_id FOREIGN KEY (org_id) REFERENCES organizations(id),
    KEY idx_audit_logs_org_created (org_id, created_at),
    KEY idx_audit_logs_target_created (target_type, target_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 刷新令牌表
CREATE TABLE refresh_tokens (
    id CHAR(36) PRIMARY KEY,
    user_id CHAR(36) NOT NULL,
    token_hash VARCHAR(255) NOT NULL UNIQUE,
    expires_at DATETIME(6) NOT NULL,
    revoked_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT fk_refresh_tokens_user_id FOREIGN KEY (user_id) REFERENCES users(id),
    KEY idx_refresh_tokens_user_expires (user_id, expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- RAGFlow 数据集映射表（org_scope_key/app_scope_key 生成列替代部分唯一索引；remote 单列 WHERE 退化为普通 UNIQUE）
CREATE TABLE ragflow_datasets (
    id CHAR(36) PRIMARY KEY,
    scope_type VARCHAR(50) NOT NULL,
    org_id CHAR(36) NOT NULL,
    app_id CHAR(36) NULL,
    ragflow_dataset_id VARCHAR(255) NULL,
    name VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    last_error TEXT NULL,
    create_claim_token VARCHAR(255) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    org_scope_key CHAR(36) GENERATED ALWAYS AS (CASE WHEN scope_type = 'org' THEN org_id END) VIRTUAL,
    app_scope_key CHAR(36) GENERATED ALWAYS AS (CASE WHEN scope_type = 'app' THEN app_id END) VIRTUAL,
    CONSTRAINT ragflow_datasets_scope_type_check CHECK (scope_type IN ('org','app')),
    CONSTRAINT ragflow_datasets_status_check CHECK (status IN ('creating','active','deleting','failed')),
    CONSTRAINT ragflow_datasets_scope_app_check CHECK (
        (scope_type = 'org' AND app_id IS NULL) OR (scope_type = 'app' AND app_id IS NOT NULL)),
    CONSTRAINT fk_ragflow_datasets_org_id FOREIGN KEY (org_id) REFERENCES organizations(id),
    CONSTRAINT fk_ragflow_datasets_app_id FOREIGN KEY (app_id) REFERENCES apps(id),
    UNIQUE KEY uk_ragflow_datasets_scope_identity (id, scope_type, org_id),
    UNIQUE KEY uk_ragflow_datasets_app_identity (id, scope_type, org_id, app_id),
    UNIQUE KEY uk_ragflow_datasets_org_unique (org_scope_key),
    UNIQUE KEY uk_ragflow_datasets_app_unique (app_scope_key),
    UNIQUE KEY uk_ragflow_datasets_remote_unique (ragflow_dataset_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- RAGFlow 文档元数据缓存表
CREATE TABLE ragflow_documents (
    id CHAR(36) PRIMARY KEY,
    dataset_id CHAR(36) NOT NULL,
    scope_type VARCHAR(50) NOT NULL,
    org_id CHAR(36) NOT NULL,
    app_id CHAR(36) NULL,
    ragflow_document_id VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL,
    size_bytes BIGINT NOT NULL DEFAULT 0,
    mime_type VARCHAR(100) NULL,
    suffix VARCHAR(50) NULL,
    parse_status VARCHAR(50) NOT NULL DEFAULT 'queued',
    progress INT NOT NULL DEFAULT 0,
    last_error TEXT NULL,
    created_by VARCHAR(255) NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    CONSTRAINT ragflow_documents_scope_type_check CHECK (scope_type IN ('org','app')),
    CONSTRAINT ragflow_documents_parse_status_check CHECK (parse_status IN ('queued','running','completed','failed','stopped')),
    CONSTRAINT ragflow_documents_progress_check CHECK (progress >= 0 AND progress <= 100),
    CONSTRAINT ragflow_documents_scope_app_check CHECK (
        (scope_type = 'org' AND app_id IS NULL) OR (scope_type = 'app' AND app_id IS NOT NULL)),
    CONSTRAINT fk_ragflow_documents_dataset_scope FOREIGN KEY (dataset_id, scope_type, org_id)
        REFERENCES ragflow_datasets(id, scope_type, org_id) ON DELETE CASCADE,
    CONSTRAINT fk_ragflow_documents_dataset_app_scope FOREIGN KEY (dataset_id, scope_type, org_id, app_id)
        REFERENCES ragflow_datasets(id, scope_type, org_id, app_id) ON DELETE CASCADE,
    CONSTRAINT fk_ragflow_documents_org_id FOREIGN KEY (org_id) REFERENCES organizations(id),
    CONSTRAINT fk_ragflow_documents_app_id FOREIGN KEY (app_id) REFERENCES apps(id),
    UNIQUE KEY uk_ragflow_documents_dataset_remote (dataset_id, ragflow_document_id),
    KEY idx_ragflow_documents_scope (scope_type, org_id, app_id, created_at DESC),
    KEY idx_ragflow_documents_parse_status (parse_status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 节点资源采样表（spec-C 保留；workstream A 删除）
CREATE TABLE node_resource_samples (
    id CHAR(36) PRIMARY KEY,
    runtime_node_id CHAR(36) NOT NULL,
    sampled_at DATETIME(6) NOT NULL,
    cpu_percent DOUBLE NULL,
    memory_used_bytes BIGINT NULL,
    memory_total_bytes BIGINT NULL,
    disk_used_bytes BIGINT NULL,
    disk_total_bytes BIGINT NULL,
    network_rx_bytes BIGINT NULL,
    network_tx_bytes BIGINT NULL,
    instance_count INT NULL,
    last_error TEXT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT fk_node_resource_samples_node FOREIGN KEY (runtime_node_id) REFERENCES runtime_nodes(id) ON DELETE CASCADE,
    KEY node_resource_samples_node_time_idx (runtime_node_id, sampled_at DESC),
    KEY node_resource_samples_sampled_at_idx (sampled_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 实例资源采样表（spec-C 保留；workstream A 删除）
CREATE TABLE instance_resource_samples (
    id CHAR(36) PRIMARY KEY,
    app_id CHAR(36) NOT NULL,
    runtime_node_id CHAR(36) NOT NULL,
    container_id VARCHAR(255) NOT NULL,
    sampled_at DATETIME(6) NOT NULL,
    container_status VARCHAR(50) NULL,
    cpu_percent DOUBLE NULL,
    memory_used_bytes BIGINT NULL,
    memory_limit_bytes BIGINT NULL,
    disk_read_bytes BIGINT NULL,
    disk_write_bytes BIGINT NULL,
    network_rx_bytes BIGINT NULL,
    network_tx_bytes BIGINT NULL,
    last_error TEXT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT fk_instance_resource_samples_app FOREIGN KEY (app_id) REFERENCES apps(id) ON DELETE CASCADE,
    CONSTRAINT fk_instance_resource_samples_node FOREIGN KEY (runtime_node_id) REFERENCES runtime_nodes(id) ON DELETE CASCADE,
    KEY instance_resource_samples_app_time_idx (app_id, sampled_at DESC),
    KEY instance_resource_samples_node_time_idx (runtime_node_id, sampled_at DESC),
    KEY instance_resource_samples_node_app_time_idx (runtime_node_id, app_id, sampled_at DESC),
    KEY instance_resource_samples_sampled_at_idx (sampled_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

SET FOREIGN_KEY_CHECKS = 1;
