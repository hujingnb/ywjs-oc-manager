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
WHERE runtime_node_id = ANY($1::uuid[])
ORDER BY runtime_node_id, sampled_at DESC, id DESC;

-- name: ListNodeResourceSamples :many
SELECT *
FROM node_resource_samples
WHERE runtime_node_id = $1
  AND sampled_at >= $2
  AND sampled_at <= $3
ORDER BY sampled_at ASC, id ASC;

-- name: ListNodeResourceBuckets :many
SELECT
    to_timestamp(floor(extract(epoch FROM sampled_at) / sqlc.arg(bucket_seconds)::integer)::bigint * sqlc.arg(bucket_seconds)::integer)::timestamptz AS sampled_at,
    avg(cpu_percent)::double precision AS cpu_percent,
    avg(memory_used_bytes)::bigint AS memory_used_bytes,
    max(memory_total_bytes)::bigint AS memory_total_bytes,
    avg(disk_used_bytes)::bigint AS disk_used_bytes,
    max(disk_total_bytes)::bigint AS disk_total_bytes,
    min(network_rx_bytes)::bigint AS network_rx_bytes,
    min(network_tx_bytes)::bigint AS network_tx_bytes,
    avg(instance_count)::integer AS instance_count,
    (array_remove(array_agg(last_error ORDER BY sampled_at DESC), NULL))[1] AS last_error
FROM node_resource_samples
WHERE runtime_node_id = $1
  AND sampled_at >= $2
  AND sampled_at <= $3
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
  AND sampled_at >= $2
  AND sampled_at <= $3
ORDER BY sampled_at ASC, id ASC;

-- name: ListNodeInstanceResourceSamples :many
SELECT *
FROM instance_resource_samples
WHERE runtime_node_id = $1
  AND app_id = $2
  AND sampled_at >= $3
  AND sampled_at <= $4
ORDER BY sampled_at ASC, id ASC;

-- name: ListInstanceResourceBuckets :many
SELECT
    to_timestamp(floor(extract(epoch FROM sampled_at) / sqlc.arg(bucket_seconds)::integer)::bigint * sqlc.arg(bucket_seconds)::integer)::timestamptz AS sampled_at,
    (array_remove(array_agg(container_status ORDER BY sampled_at DESC), NULL))[1] AS container_status,
    avg(cpu_percent)::double precision AS cpu_percent,
    avg(memory_used_bytes)::bigint AS memory_used_bytes,
    max(memory_limit_bytes)::bigint AS memory_limit_bytes,
    min(disk_read_bytes)::bigint AS disk_read_bytes,
    min(disk_write_bytes)::bigint AS disk_write_bytes,
    min(network_rx_bytes)::bigint AS network_rx_bytes,
    min(network_tx_bytes)::bigint AS network_tx_bytes,
    (array_remove(array_agg(last_error ORDER BY sampled_at DESC), NULL))[1] AS last_error
FROM instance_resource_samples
WHERE app_id = $1
  AND sampled_at >= $2
  AND sampled_at <= $3
GROUP BY 1
ORDER BY 1 ASC;

-- name: DeleteOldNodeResourceSamples :execrows
DELETE FROM node_resource_samples
WHERE id IN (
    SELECT s.id
    FROM node_resource_samples AS s
    WHERE s.sampled_at < $1
    ORDER BY s.sampled_at ASC
    LIMIT $2
);

-- name: DeleteOldInstanceResourceSamples :execrows
DELETE FROM instance_resource_samples
WHERE id IN (
    SELECT s.id
    FROM instance_resource_samples AS s
    WHERE s.sampled_at < $1
    ORDER BY s.sampled_at ASC
    LIMIT $2
);
