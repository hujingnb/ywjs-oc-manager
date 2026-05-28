-- name: InsertNodeResourceSample :exec
INSERT INTO node_resource_samples (
    id, runtime_node_id, sampled_at, cpu_percent,
    memory_used_bytes, memory_total_bytes,
    disk_used_bytes, disk_total_bytes,
    network_rx_bytes, network_tx_bytes,
    instance_count, last_error
) VALUES (
    ?, ?, ?, ?,
    ?, ?,
    ?, ?,
    ?, ?,
    ?, ?
);

-- name: InsertInstanceResourceSample :exec
INSERT INTO instance_resource_samples (
    id, app_id, runtime_node_id, container_id, sampled_at, container_status,
    cpu_percent, memory_used_bytes, memory_limit_bytes,
    disk_read_bytes, disk_write_bytes,
    network_rx_bytes, network_tx_bytes, last_error
) VALUES (
    ?, ?, ?, ?, ?, ?,
    ?, ?, ?,
    ?, ?,
    ?, ?, ?
);

-- name: GetLatestNodeResourceSample :one
SELECT *
FROM node_resource_samples
WHERE runtime_node_id = ?
ORDER BY sampled_at DESC, id DESC
LIMIT 1;

-- name: ListLatestNodeResourceSamples :many
SELECT s.id, s.runtime_node_id, s.sampled_at, s.cpu_percent, s.memory_used_bytes,
       s.memory_total_bytes, s.disk_used_bytes, s.disk_total_bytes,
       s.network_rx_bytes, s.network_tx_bytes, s.instance_count, s.last_error, s.created_at
FROM (
    SELECT *, ROW_NUMBER() OVER (PARTITION BY runtime_node_id ORDER BY sampled_at DESC, id DESC) AS rn
    FROM node_resource_samples
    WHERE runtime_node_id IN (sqlc.slice(runtime_node_ids))
) AS s
WHERE s.rn = 1;

-- name: ListNodeResourceSamples :many
SELECT *
FROM node_resource_samples
WHERE runtime_node_id = ?
  AND sampled_at >= sqlc.arg(from_sampled_at)
  AND sampled_at <= sqlc.arg(to_sampled_at)
ORDER BY sampled_at ASC, id ASC;

-- name: ListNodeResourceBuckets :many
SELECT
    CAST(FROM_UNIXTIME(FLOOR(UNIX_TIMESTAMP(sampled_at) / CAST(sqlc.arg(bucket_seconds) AS SIGNED)) * CAST(sqlc.arg(bucket_seconds) AS SIGNED)) AS DATETIME) AS sampled_at,
    CAST(COALESCE(AVG(cpu_percent), 0) AS DOUBLE) AS cpu_percent,
    COUNT(cpu_percent) > 0 AS has_cpu_percent,
    CAST(COALESCE(AVG(memory_used_bytes), 0) AS SIGNED) AS memory_used_bytes,
    COUNT(memory_used_bytes) > 0 AS has_memory_used_bytes,
    CAST(COALESCE(MAX(memory_total_bytes), 0) AS SIGNED) AS memory_total_bytes,
    COUNT(memory_total_bytes) > 0 AS has_memory_total_bytes,
    CAST(COALESCE(AVG(disk_used_bytes), 0) AS SIGNED) AS disk_used_bytes,
    COUNT(disk_used_bytes) > 0 AS has_disk_used_bytes,
    CAST(COALESCE(MAX(disk_total_bytes), 0) AS SIGNED) AS disk_total_bytes,
    COUNT(disk_total_bytes) > 0 AS has_disk_total_bytes,
    CAST(COALESCE(MIN(network_rx_bytes), 0) AS SIGNED) AS network_rx_bytes,
    COUNT(network_rx_bytes) > 0 AS has_network_rx_bytes,
    CAST(COALESCE(MIN(network_tx_bytes), 0) AS SIGNED) AS network_tx_bytes,
    COUNT(network_tx_bytes) > 0 AS has_network_tx_bytes,
    CAST(COALESCE(AVG(instance_count), 0) AS SIGNED) AS instance_count,
    COUNT(instance_count) > 0 AS has_instance_count,
    COALESCE(SUBSTRING_INDEX(GROUP_CONCAT(last_error ORDER BY sampled_at DESC SEPARATOR '\x1e'), '\x1e', 1), '') AS last_error,
    COUNT(last_error) > 0 AS has_last_error
FROM node_resource_samples
WHERE runtime_node_id = ?
  AND sampled_at >= sqlc.arg(from_sampled_at)
  AND sampled_at <= sqlc.arg(to_sampled_at)
GROUP BY 1
ORDER BY 1 ASC;

-- name: GetLatestInstanceResourceSample :one
SELECT *
FROM instance_resource_samples
WHERE app_id = ?
ORDER BY sampled_at DESC, id DESC
LIMIT 1;

-- name: ListLatestInstanceResourceSamplesByNode :many
SELECT s.id, s.app_id, s.runtime_node_id, s.container_id, s.sampled_at, s.container_status,
       s.cpu_percent, s.memory_used_bytes, s.memory_limit_bytes, s.disk_read_bytes,
       s.disk_write_bytes, s.network_rx_bytes, s.network_tx_bytes, s.last_error, s.created_at
FROM (
    SELECT *, ROW_NUMBER() OVER (PARTITION BY app_id ORDER BY sampled_at DESC, id DESC) AS rn
    FROM instance_resource_samples
    WHERE runtime_node_id = ?
) AS s
WHERE s.rn = 1;

-- name: ListInstanceResourceSamples :many
SELECT *
FROM instance_resource_samples
WHERE app_id = ?
  AND sampled_at >= sqlc.arg(from_sampled_at)
  AND sampled_at <= sqlc.arg(to_sampled_at)
ORDER BY sampled_at ASC, id ASC;

-- name: ListNodeInstanceResourceSamples :many
SELECT *
FROM instance_resource_samples
WHERE runtime_node_id = ?
  AND app_id = ?
  AND sampled_at >= sqlc.arg(from_sampled_at)
  AND sampled_at <= sqlc.arg(to_sampled_at)
ORDER BY sampled_at ASC, id ASC;

-- name: ListInstanceResourceBuckets :many
SELECT
    CAST(FROM_UNIXTIME(FLOOR(UNIX_TIMESTAMP(sampled_at) / CAST(sqlc.arg(bucket_seconds) AS SIGNED)) * CAST(sqlc.arg(bucket_seconds) AS SIGNED)) AS DATETIME) AS sampled_at,
    COALESCE(SUBSTRING_INDEX(GROUP_CONCAT(container_status ORDER BY sampled_at DESC SEPARATOR '\x1e'), '\x1e', 1), '') AS container_status,
    COUNT(container_status) > 0 AS has_container_status,
    CAST(COALESCE(AVG(cpu_percent), 0) AS DOUBLE) AS cpu_percent,
    COUNT(cpu_percent) > 0 AS has_cpu_percent,
    CAST(COALESCE(AVG(memory_used_bytes), 0) AS SIGNED) AS memory_used_bytes,
    COUNT(memory_used_bytes) > 0 AS has_memory_used_bytes,
    CAST(COALESCE(MAX(memory_limit_bytes), 0) AS SIGNED) AS memory_limit_bytes,
    COUNT(memory_limit_bytes) > 0 AS has_memory_limit_bytes,
    CAST(COALESCE(MIN(disk_read_bytes), 0) AS SIGNED) AS disk_read_bytes,
    COUNT(disk_read_bytes) > 0 AS has_disk_read_bytes,
    CAST(COALESCE(MIN(disk_write_bytes), 0) AS SIGNED) AS disk_write_bytes,
    COUNT(disk_write_bytes) > 0 AS has_disk_write_bytes,
    CAST(COALESCE(MIN(network_rx_bytes), 0) AS SIGNED) AS network_rx_bytes,
    COUNT(network_rx_bytes) > 0 AS has_network_rx_bytes,
    CAST(COALESCE(MIN(network_tx_bytes), 0) AS SIGNED) AS network_tx_bytes,
    COUNT(network_tx_bytes) > 0 AS has_network_tx_bytes,
    COALESCE(SUBSTRING_INDEX(GROUP_CONCAT(last_error ORDER BY sampled_at DESC SEPARATOR '\x1e'), '\x1e', 1), '') AS last_error,
    COUNT(last_error) > 0 AS has_last_error
FROM instance_resource_samples
WHERE app_id = ?
  AND sampled_at >= sqlc.arg(from_sampled_at)
  AND sampled_at <= sqlc.arg(to_sampled_at)
GROUP BY 1
ORDER BY 1 ASC;

-- name: ListNodeInstanceResourceBuckets :many
SELECT
    CAST(FROM_UNIXTIME(FLOOR(UNIX_TIMESTAMP(sampled_at) / CAST(sqlc.arg(bucket_seconds) AS SIGNED)) * CAST(sqlc.arg(bucket_seconds) AS SIGNED)) AS DATETIME) AS sampled_at,
    COALESCE(SUBSTRING_INDEX(GROUP_CONCAT(container_status ORDER BY sampled_at DESC SEPARATOR '\x1e'), '\x1e', 1), '') AS container_status,
    COUNT(container_status) > 0 AS has_container_status,
    CAST(COALESCE(AVG(cpu_percent), 0) AS DOUBLE) AS cpu_percent,
    COUNT(cpu_percent) > 0 AS has_cpu_percent,
    CAST(COALESCE(AVG(memory_used_bytes), 0) AS SIGNED) AS memory_used_bytes,
    COUNT(memory_used_bytes) > 0 AS has_memory_used_bytes,
    CAST(COALESCE(MAX(memory_limit_bytes), 0) AS SIGNED) AS memory_limit_bytes,
    COUNT(memory_limit_bytes) > 0 AS has_memory_limit_bytes,
    CAST(COALESCE(MIN(disk_read_bytes), 0) AS SIGNED) AS disk_read_bytes,
    COUNT(disk_read_bytes) > 0 AS has_disk_read_bytes,
    CAST(COALESCE(MIN(disk_write_bytes), 0) AS SIGNED) AS disk_write_bytes,
    COUNT(disk_write_bytes) > 0 AS has_disk_write_bytes,
    CAST(COALESCE(MIN(network_rx_bytes), 0) AS SIGNED) AS network_rx_bytes,
    COUNT(network_rx_bytes) > 0 AS has_network_rx_bytes,
    CAST(COALESCE(MIN(network_tx_bytes), 0) AS SIGNED) AS network_tx_bytes,
    COUNT(network_tx_bytes) > 0 AS has_network_tx_bytes,
    COALESCE(SUBSTRING_INDEX(GROUP_CONCAT(last_error ORDER BY sampled_at DESC SEPARATOR '\x1e'), '\x1e', 1), '') AS last_error,
    COUNT(last_error) > 0 AS has_last_error
FROM instance_resource_samples
WHERE runtime_node_id = ?
  AND app_id = ?
  AND sampled_at >= sqlc.arg(from_sampled_at)
  AND sampled_at <= sqlc.arg(to_sampled_at)
GROUP BY 1
ORDER BY 1 ASC;

-- name: DeleteOldNodeResourceSamples :execrows
-- 批量清理过期节点采样，避免全表 DELETE 锁争用；
-- MySQL 不支持 LIMIT 直接用于 IN 子查询，故用双层派生表绕过限制。
DELETE FROM node_resource_samples
WHERE id IN (
    SELECT id FROM (
        SELECT s.id
        FROM node_resource_samples AS s
        WHERE s.sampled_at < sqlc.arg(cutoff_sampled_at)
        ORDER BY s.sampled_at ASC
        LIMIT ?
    ) AS x
);

-- name: DeleteOldInstanceResourceSamples :execrows
-- 批量清理过期实例采样，避免全表 DELETE 锁争用；
-- MySQL 不支持 LIMIT 直接用于 IN 子查询，故用双层派生表绕过限制。
DELETE FROM instance_resource_samples
WHERE id IN (
    SELECT id FROM (
        SELECT s.id
        FROM instance_resource_samples AS s
        WHERE s.sampled_at < sqlc.arg(cutoff_sampled_at)
        ORDER BY s.sampled_at ASC
        LIMIT ?
    ) AS x
);
