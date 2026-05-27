-- name: CreateRAGFlowOrgDatasetMapping :one
-- 懒创建组织级 dataset 映射；并发首创命中部分唯一索引时不返回行，由 service 读取已有映射且不重复创建远端 dataset。
INSERT INTO ragflow_datasets (
    scope_type, org_id, app_id, ragflow_dataset_id, name, status, last_error, create_claim_token
) VALUES (
    'org', sqlc.arg(org_id), NULL, NULL, sqlc.arg(name), 'creating', NULL, sqlc.arg(create_claim_token)::text
)
ON CONFLICT (org_id) WHERE scope_type = 'org' DO NOTHING
RETURNING *;

-- name: CreateRAGFlowAppDatasetMapping :one
-- 懒创建实例级 dataset 映射；并发首创命中部分唯一索引时不返回行，由 service 读取已有映射且不重复创建远端 dataset。
INSERT INTO ragflow_datasets (
    scope_type, org_id, app_id, ragflow_dataset_id, name, status, last_error, create_claim_token
) VALUES (
    'app', sqlc.arg(org_id), sqlc.arg(app_id), NULL, sqlc.arg(name), 'creating', NULL, sqlc.arg(create_claim_token)::text
)
ON CONFLICT (app_id) WHERE scope_type = 'app' DO NOTHING
RETURNING *;

-- name: ClaimRAGFlowDatasetCreation :one
-- 抢占 failed 或超时 creating 的 dataset 创建租约；只有返回行的调用方允许访问 RAGFlow 创建远端 dataset。
UPDATE ragflow_datasets
SET status = 'creating',
    last_error = NULL,
    create_claim_token = sqlc.arg(create_claim_token)::text,
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND (
    status = 'failed'
    OR (status = 'creating' AND updated_at < sqlc.arg(stale_before)::timestamptz)
  )
RETURNING *;

-- name: SetRAGFlowDatasetActive :one
-- 远端 dataset 创建成功后写入 RAGFlow ID，并清理上一轮生命周期错误。
UPDATE ragflow_datasets
SET ragflow_dataset_id = $2,
    name = $3,
    status = 'active',
    last_error = NULL,
    create_claim_token = NULL,
    updated_at = now()
WHERE id = $1
  AND status = 'creating'
  AND create_claim_token = $4
RETURNING *;

-- name: GetRAGFlowOrgDataset :one
-- 读取组织知识库 dataset 映射，供管理面列表和 runtime 检索使用。
SELECT *
FROM ragflow_datasets
WHERE scope_type = 'org' AND org_id = $1;

-- name: GetRAGFlowAppDataset :one
-- 读取实例知识库 dataset 映射，runtime 写入只能落到该 dataset。
SELECT *
FROM ragflow_datasets
WHERE scope_type = 'app' AND app_id = $1;

-- name: MarkRAGFlowDatasetFailed :one
-- 标记 dataset 生命周期失败，保留错误文本用于管理面排障。
UPDATE ragflow_datasets
SET status = 'failed',
    last_error = $2,
    create_claim_token = NULL,
    updated_at = now()
WHERE id = $1
  AND status = 'creating'
  AND create_claim_token = $3
RETURNING *;

-- name: DeleteRAGFlowDatasetMapping :exec
-- 删除本地 dataset 映射；document 缓存通过外键级联清理。
DELETE FROM ragflow_datasets
WHERE id = $1;

-- name: CreateRAGFlowDocument :one
-- 缓存 RAGFlow document 元数据，manager 不保存文件主副本。
INSERT INTO ragflow_documents (
    dataset_id, scope_type, org_id, app_id, ragflow_document_id, name,
    size_bytes, mime_type, suffix, parse_status, progress, last_error, created_by
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11, $12, $13
)
RETURNING *;

-- name: ListRAGFlowDocumentsByScope :many
-- 扁平列出某个组织或实例知识库文件，支持按状态和文件名过滤。
SELECT *
FROM ragflow_documents
WHERE scope_type = $1
  AND org_id = $2
  AND (sqlc.narg(app_id)::uuid IS NULL OR app_id = sqlc.narg(app_id)::uuid)
  AND (sqlc.narg(parse_status)::text IS NULL OR parse_status = sqlc.narg(parse_status)::text)
  AND (sqlc.narg(keywords)::text IS NULL OR name ILIKE '%' || sqlc.narg(keywords)::text || '%')
ORDER BY created_at DESC, id DESC
LIMIT $3 OFFSET $4;

-- name: CountRAGFlowDocumentsByScope :one
-- 统计扁平文件列表总数，过滤条件必须与 ListRAGFlowDocumentsByScope 保持一致。
SELECT count(*)
FROM ragflow_documents
WHERE scope_type = $1
  AND org_id = $2
  AND (sqlc.narg(app_id)::uuid IS NULL OR app_id = sqlc.narg(app_id)::uuid)
  AND (sqlc.narg(parse_status)::text IS NULL OR parse_status = sqlc.narg(parse_status)::text)
  AND (sqlc.narg(keywords)::text IS NULL OR name ILIKE '%' || sqlc.narg(keywords)::text || '%');

-- name: GetRAGFlowDocument :one
-- 按 manager 本地 ID 读取 document 缓存，供下载和删除前做权限校验。
SELECT *
FROM ragflow_documents
WHERE id = $1;

-- name: GetRAGFlowDocumentByRemoteID :one
-- 按 RAGFlow document ID 读取缓存，用于解析状态回刷和幂等处理。
SELECT *
FROM ragflow_documents
WHERE dataset_id = $1 AND ragflow_document_id = $2;

-- name: UpdateRAGFlowDocumentParseStatus :one
-- 回写解析状态、进度和错误；状态值由 service 层从 RAGFlow run 值归一化。
UPDATE ragflow_documents
SET parse_status = $2, progress = $3, last_error = $4, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteRAGFlowDocumentMapping :exec
-- 删除本地 document 缓存；RAGFlow 远端删除由 service 在同一业务流程中处理。
DELETE FROM ragflow_documents
WHERE id = $1;

-- name: ListRAGFlowDocumentsNeedingRefresh :many
-- 找出需要刷新解析状态的 document，按最久未更新优先。
SELECT *
FROM ragflow_documents
WHERE parse_status IN ('queued', 'running')
ORDER BY updated_at ASC
LIMIT $1;
