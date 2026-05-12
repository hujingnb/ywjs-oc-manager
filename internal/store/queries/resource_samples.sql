-- name: InsertNodeResourceSample :one
INSERT INTO node_resource_samples (
    runtime_node_id, sampled_at, cpu_percent,
    memory_used_bytes, memory_total_bytes,
    disk_used_bytes, disk_total_bytes,
    network_rx_bytes, network_tx_bytes,
    instance_count, last_error
) VALUES (
    $1, $2, $3,
    $4, $5,
    $6, $7,
    $8, $9,
    $10, $11
)
RETURNING *;

-- name: InsertInstanceResourceSample :one
INSERT INTO instance_resource_samples (
    app_id, runtime_node_id, container_id, sampled_at, container_status,
    cpu_percent, memory_used_bytes, memory_limit_bytes,
    disk_read_bytes, disk_write_bytes,
    network_rx_bytes, network_tx_bytes, last_error
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8,
    $9, $10,
    $11, $12, $13
)
RETURNING *;

-- name: GetLatestNodeResourceSample :one
SELECT *
FROM node_resource_samples
WHERE runtime_node_id = $1
ORDER BY sampled_at DESC, id DESC
LIMIT 1;

-- name: ListLatestNodeResourceSamples :many
SELECT DISTINCT ON (runtime_node_id) *
FROM node_resource_samples
WHERE runtime_node_id = ANY(sqlc.arg(runtime_node_ids)::uuid[])
ORDER BY runtime_node_id, sampled_at DESC, id DESC;

-- name: ListNodeResourceSamples :many
SELECT *
FROM node_resource_samples
WHERE runtime_node_id = $1
  AND sampled_at >= sqlc.arg(from_sampled_at)
  AND sampled_at <= sqlc.arg(to_sampled_at)
ORDER BY sampled_at ASC, id ASC;

-- name: ListNodeResourceBuckets :many
SELECT
    to_timestamp(floor(extract(epoch FROM sampled_at) / sqlc.arg(bucket_seconds)::integer)::bigint * sqlc.arg(bucket_seconds)::integer)::timestamptz AS sampled_at,
    COALESCE(avg(cpu_percent)::double precision, 0::double precision)::double precision AS cpu_percent,
    count(cpu_percent) > 0 AS has_cpu_percent,
    COALESCE(avg(memory_used_bytes)::bigint, 0::bigint)::bigint AS memory_used_bytes,
    count(memory_used_bytes) > 0 AS has_memory_used_bytes,
    COALESCE(max(memory_total_bytes)::bigint, 0::bigint)::bigint AS memory_total_bytes,
    count(memory_total_bytes) > 0 AS has_memory_total_bytes,
    COALESCE(avg(disk_used_bytes)::bigint, 0::bigint)::bigint AS disk_used_bytes,
    count(disk_used_bytes) > 0 AS has_disk_used_bytes,
    COALESCE(max(disk_total_bytes)::bigint, 0::bigint)::bigint AS disk_total_bytes,
    count(disk_total_bytes) > 0 AS has_disk_total_bytes,
    COALESCE(min(network_rx_bytes)::bigint, 0::bigint)::bigint AS network_rx_bytes,
    count(network_rx_bytes) > 0 AS has_network_rx_bytes,
    COALESCE(min(network_tx_bytes)::bigint, 0::bigint)::bigint AS network_tx_bytes,
    count(network_tx_bytes) > 0 AS has_network_tx_bytes,
    COALESCE(avg(instance_count)::integer, 0::integer)::integer AS instance_count,
    count(instance_count) > 0 AS has_instance_count,
    COALESCE(((array_remove(array_agg(last_error ORDER BY sampled_at DESC), NULL))[1])::text, ''::text)::text AS last_error,
    count(last_error) > 0 AS has_last_error
FROM node_resource_samples
WHERE runtime_node_id = $1
  AND sampled_at >= sqlc.arg(from_sampled_at)
  AND sampled_at <= sqlc.arg(to_sampled_at)
GROUP BY 1
ORDER BY 1 ASC;

-- name: GetLatestInstanceResourceSample :one
SELECT *
FROM instance_resource_samples
WHERE app_id = $1
ORDER BY sampled_at DESC, id DESC
LIMIT 1;

-- name: ListLatestInstanceResourceSamplesByNode :many
SELECT DISTINCT ON (app_id) *
FROM instance_resource_samples
WHERE runtime_node_id = $1
ORDER BY app_id, sampled_at DESC, id DESC;

-- name: ListInstanceResourceSamples :many
SELECT *
FROM instance_resource_samples
WHERE app_id = $1
  AND sampled_at >= sqlc.arg(from_sampled_at)
  AND sampled_at <= sqlc.arg(to_sampled_at)
ORDER BY sampled_at ASC, id ASC;

-- name: ListNodeInstanceResourceSamples :many
SELECT *
FROM instance_resource_samples
WHERE runtime_node_id = $1
  AND app_id = $2
  AND sampled_at >= sqlc.arg(from_sampled_at)
  AND sampled_at <= sqlc.arg(to_sampled_at)
ORDER BY sampled_at ASC, id ASC;

-- name: ListInstanceResourceBuckets :many
SELECT
    to_timestamp(floor(extract(epoch FROM sampled_at) / sqlc.arg(bucket_seconds)::integer)::bigint * sqlc.arg(bucket_seconds)::integer)::timestamptz AS sampled_at,
    COALESCE(((array_remove(array_agg(container_status ORDER BY sampled_at DESC), NULL))[1])::text, ''::text)::text AS container_status,
    count(container_status) > 0 AS has_container_status,
    COALESCE(avg(cpu_percent)::double precision, 0::double precision)::double precision AS cpu_percent,
    count(cpu_percent) > 0 AS has_cpu_percent,
    COALESCE(avg(memory_used_bytes)::bigint, 0::bigint)::bigint AS memory_used_bytes,
    count(memory_used_bytes) > 0 AS has_memory_used_bytes,
    COALESCE(max(memory_limit_bytes)::bigint, 0::bigint)::bigint AS memory_limit_bytes,
    count(memory_limit_bytes) > 0 AS has_memory_limit_bytes,
    COALESCE(min(disk_read_bytes)::bigint, 0::bigint)::bigint AS disk_read_bytes,
    count(disk_read_bytes) > 0 AS has_disk_read_bytes,
    COALESCE(min(disk_write_bytes)::bigint, 0::bigint)::bigint AS disk_write_bytes,
    count(disk_write_bytes) > 0 AS has_disk_write_bytes,
    COALESCE(min(network_rx_bytes)::bigint, 0::bigint)::bigint AS network_rx_bytes,
    count(network_rx_bytes) > 0 AS has_network_rx_bytes,
    COALESCE(min(network_tx_bytes)::bigint, 0::bigint)::bigint AS network_tx_bytes,
    count(network_tx_bytes) > 0 AS has_network_tx_bytes,
    COALESCE(((array_remove(array_agg(last_error ORDER BY sampled_at DESC), NULL))[1])::text, ''::text)::text AS last_error,
    count(last_error) > 0 AS has_last_error
FROM instance_resource_samples
WHERE app_id = $1
  AND sampled_at >= sqlc.arg(from_sampled_at)
  AND sampled_at <= sqlc.arg(to_sampled_at)
GROUP BY 1
ORDER BY 1 ASC;

-- name: ListNodeInstanceResourceBuckets :many
SELECT
    to_timestamp(floor(extract(epoch FROM sampled_at) / sqlc.arg(bucket_seconds)::integer)::bigint * sqlc.arg(bucket_seconds)::integer)::timestamptz AS sampled_at,
    COALESCE(((array_remove(array_agg(container_status ORDER BY sampled_at DESC), NULL))[1])::text, ''::text)::text AS container_status,
    count(container_status) > 0 AS has_container_status,
    COALESCE(avg(cpu_percent)::double precision, 0::double precision)::double precision AS cpu_percent,
    count(cpu_percent) > 0 AS has_cpu_percent,
    COALESCE(avg(memory_used_bytes)::bigint, 0::bigint)::bigint AS memory_used_bytes,
    count(memory_used_bytes) > 0 AS has_memory_used_bytes,
    COALESCE(max(memory_limit_bytes)::bigint, 0::bigint)::bigint AS memory_limit_bytes,
    count(memory_limit_bytes) > 0 AS has_memory_limit_bytes,
    COALESCE(min(disk_read_bytes)::bigint, 0::bigint)::bigint AS disk_read_bytes,
    count(disk_read_bytes) > 0 AS has_disk_read_bytes,
    COALESCE(min(disk_write_bytes)::bigint, 0::bigint)::bigint AS disk_write_bytes,
    count(disk_write_bytes) > 0 AS has_disk_write_bytes,
    COALESCE(min(network_rx_bytes)::bigint, 0::bigint)::bigint AS network_rx_bytes,
    count(network_rx_bytes) > 0 AS has_network_rx_bytes,
    COALESCE(min(network_tx_bytes)::bigint, 0::bigint)::bigint AS network_tx_bytes,
    count(network_tx_bytes) > 0 AS has_network_tx_bytes,
    COALESCE(((array_remove(array_agg(last_error ORDER BY sampled_at DESC), NULL))[1])::text, ''::text)::text AS last_error,
    count(last_error) > 0 AS has_last_error
FROM instance_resource_samples
WHERE runtime_node_id = $1
  AND app_id = $2
  AND sampled_at >= sqlc.arg(from_sampled_at)
  AND sampled_at <= sqlc.arg(to_sampled_at)
GROUP BY 1
ORDER BY 1 ASC;

-- name: DeleteOldNodeResourceSamples :execrows
DELETE FROM node_resource_samples
WHERE id IN (
    SELECT s.id
    FROM node_resource_samples AS s
    WHERE s.sampled_at < sqlc.arg(cutoff_sampled_at)
    ORDER BY s.sampled_at ASC
    LIMIT sqlc.arg(batch_size)::integer
);

-- name: DeleteOldInstanceResourceSamples :execrows
DELETE FROM instance_resource_samples
WHERE id IN (
    SELECT s.id
    FROM instance_resource_samples AS s
    WHERE s.sampled_at < sqlc.arg(cutoff_sampled_at)
    ORDER BY s.sampled_at ASC
    LIMIT sqlc.arg(batch_size)::integer
);
