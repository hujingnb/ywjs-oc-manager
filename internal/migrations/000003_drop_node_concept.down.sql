-- spec-A2b 回滚：destructive——down 仅恢复表/列结构，不恢复数据。
-- 重建顺序：runtime_nodes 先建（无外键依赖），再建两张有外键的采样表，最后给 apps 补列。

-- 运行节点表（spec-C 保留；workstream A 已删，此 down 仅恢复结构）
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

-- 节点资源采样表
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

-- 实例资源采样表
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

-- 恢复 apps 三列（000002 语义：runtime_node_id 可空）及其外键与索引
ALTER TABLE apps
    ADD COLUMN runtime_node_id CHAR(36) NULL,
    ADD COLUMN container_id VARCHAR(255) NULL,
    ADD COLUMN container_name VARCHAR(255) NULL;
ALTER TABLE apps ADD CONSTRAINT fk_apps_runtime_node_id FOREIGN KEY (runtime_node_id) REFERENCES runtime_nodes(id);
ALTER TABLE apps ADD KEY idx_apps_runtime_node_status (runtime_node_id, status);
