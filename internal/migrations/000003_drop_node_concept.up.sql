-- spec-A2b：破坏性删除 runtime-agent 节点概念的表与 apps 节点列。
-- apps.runtime_node_id 上有外键 fk_apps_runtime_node_id 和普通索引 idx_apps_runtime_node_status，
-- MySQL 删列前必须先删对应外键和索引，否则报错。
-- container_id / container_name 列无单独索引，直接 DROP。
ALTER TABLE apps DROP FOREIGN KEY fk_apps_runtime_node_id;
ALTER TABLE apps DROP INDEX idx_apps_runtime_node_status;
ALTER TABLE apps
    DROP COLUMN runtime_node_id,
    DROP COLUMN container_id,
    DROP COLUMN container_name;

-- instance_resource_samples 对 runtime_nodes 和 apps 均有外键（ON DELETE CASCADE），
-- node_resource_samples 对 runtime_nodes 有外键（ON DELETE CASCADE）。
-- 先删两张采样表（依赖 runtime_nodes），再删 runtime_nodes（依赖已删）。
DROP TABLE IF EXISTS instance_resource_samples;
DROP TABLE IF EXISTS node_resource_samples;
DROP TABLE IF EXISTS runtime_nodes;
