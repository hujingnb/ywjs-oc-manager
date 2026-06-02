-- name: CreateRAGFlowOrgDatasetMapping :exec
-- 懒创建组织级 dataset 映射；并发首创命中唯一索引时忽略，由 service 读取已有映射且不重复创建远端 dataset。
INSERT IGNORE INTO ragflow_datasets (
    id, scope_type, org_id, app_id, ragflow_dataset_id, name, status, last_error, create_claim_token
) VALUES (
    sqlc.arg(id), 'org', sqlc.arg(org_id), NULL, NULL, sqlc.arg(name), 'creating', NULL, sqlc.arg(create_claim_token)
);

-- name: GetRAGFlowOrgDataset :one
-- 读取组织知识库 dataset 映射，供管理面列表和 runtime 检索使用。
SELECT *
FROM ragflow_datasets
WHERE scope_type = 'org' AND org_id = ?;

-- name: CreateRAGFlowAppDatasetMapping :exec
-- 懒创建实例级 dataset 映射；并发首创命中唯一索引时忽略，由 service 读取已有映射且不重复创建远端 dataset。
INSERT IGNORE INTO ragflow_datasets (
    id, scope_type, org_id, app_id, ragflow_dataset_id, name, status, last_error, create_claim_token
) VALUES (
    sqlc.arg(id), 'app', sqlc.arg(org_id), sqlc.arg(app_id), NULL, sqlc.arg(name), 'creating', NULL, sqlc.arg(create_claim_token)
);

-- name: GetRAGFlowAppDataset :one
-- 读取实例知识库 dataset 映射，runtime 写入只能落到该 dataset。
SELECT *
FROM ragflow_datasets
WHERE scope_type = 'app' AND app_id = ?;

-- name: ClaimRAGFlowDatasetCreation :exec
-- 抢占 failed 或超时 creating 的 dataset 创建租约；只有成功更新行的调用方允许访问 RAGFlow 创建远端 dataset。
UPDATE ragflow_datasets
SET status = 'creating',
    last_error = NULL,
    create_claim_token = sqlc.arg(create_claim_token),
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND (
    status = 'failed'
    OR (status = 'creating' AND updated_at < sqlc.arg(stale_before))
  );

-- name: GetRAGFlowDataset :one
-- 按 ID 读取 dataset 记录，供 ClaimRAGFlowDatasetCreation 后的读回。
SELECT *
FROM ragflow_datasets
WHERE id = ?;

-- name: SetRAGFlowDatasetActive :exec
-- 远端 dataset 创建成功后写入 RAGFlow ID，并清理上一轮生命周期错误。
UPDATE ragflow_datasets
SET ragflow_dataset_id = ?,
    name = ?,
    status = 'active',
    last_error = NULL,
    create_claim_token = NULL,
    updated_at = now()
WHERE id = ?
  AND status = 'creating'
  AND create_claim_token = ?;

-- name: MarkRAGFlowDatasetFailed :exec
-- 标记 dataset 生命周期失败，保留错误文本用于管理面排障。
UPDATE ragflow_datasets
SET status = 'failed',
    last_error = ?,
    create_claim_token = NULL,
    updated_at = now()
WHERE id = ?
  AND status = 'creating'
  AND create_claim_token = ?;

-- name: DeleteRAGFlowDatasetMapping :exec
-- 删除本地 dataset 映射；document 缓存通过外键级联清理。
DELETE FROM ragflow_datasets
WHERE id = ?;

-- name: CreateRAGFlowDocument :exec
-- 缓存 RAGFlow document 元数据，manager 不保存文件主副本。
INSERT INTO ragflow_documents (
    id, dataset_id, scope_type, org_id, app_id, ragflow_document_id, name,
    size_bytes, mime_type, suffix, parse_status, progress, last_error, created_by
) VALUES (
    ?, ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?, ?, ?, ?
);

-- name: GetRAGFlowDocument :one
-- 按 manager 本地 ID 读取 document 缓存，供下载和删除前做权限校验。
SELECT *
FROM ragflow_documents
WHERE id = ?;

-- name: ListRAGFlowDocumentsByScope :many
-- 扁平列出某个组织或实例知识库文件，支持按状态和文件名过滤。
SELECT *
FROM ragflow_documents
WHERE scope_type = ?
  AND org_id = ?
  AND (sqlc.narg(app_id) IS NULL OR app_id = sqlc.narg(app_id))
  AND (sqlc.narg(parse_status) IS NULL OR parse_status = sqlc.narg(parse_status))
  AND (sqlc.narg(keywords) IS NULL OR name LIKE CONCAT('%', sqlc.narg(keywords), '%'))
ORDER BY created_at DESC, id DESC
LIMIT ? OFFSET ?;

-- name: CountRAGFlowDocumentsByScope :one
-- 统计扁平文件列表总数，过滤条件必须与 ListRAGFlowDocumentsByScope 保持一致。
SELECT count(*)
FROM ragflow_documents
WHERE scope_type = ?
  AND org_id = ?
  AND (sqlc.narg(app_id) IS NULL OR app_id = sqlc.narg(app_id))
  AND (sqlc.narg(parse_status) IS NULL OR parse_status = sqlc.narg(parse_status))
  AND (sqlc.narg(keywords) IS NULL OR name LIKE CONCAT('%', sqlc.narg(keywords), '%'));

-- name: GetRAGFlowDocumentByRemoteID :one
-- 按 RAGFlow document ID 读取缓存，用于解析状态回刷和幂等处理。
SELECT *
FROM ragflow_documents
WHERE dataset_id = ? AND ragflow_document_id = ?;

-- name: UpdateRAGFlowDocumentParseStatus :exec
-- 回写解析状态、进度和错误；状态值由 service 层从 RAGFlow run 值归一化。
UPDATE ragflow_documents
SET parse_status = ?, progress = ?, last_error = ?, updated_at = now()
WHERE id = ?;

-- name: DeleteRAGFlowDocumentMapping :exec
-- 删除本地 document 缓存；RAGFlow 远端删除由 service 在同一业务流程中处理。
DELETE FROM ragflow_documents
WHERE id = ?;

-- name: ListRAGFlowDocumentsNeedingRefresh :many
-- 找出需要刷新解析状态的 document，按最久未更新优先；
-- 同时连出所属 RAGFlow dataset 的远端 ID，供后台刷新任务直接调 RAGFlow ListDocuments。
-- 远端 dataset 尚未创建（ragflow_dataset_id IS NULL）的文档不会出现：
-- 此类文档此时本就无法从 RAGFlow 拉取状态，等 dataset 创建完成后再轮询即可。
SELECT d.*, ds.ragflow_dataset_id AS remote_dataset_id
FROM ragflow_documents d
JOIN ragflow_datasets ds ON ds.id = d.dataset_id
WHERE d.parse_status IN ('queued', 'running')
  AND ds.ragflow_dataset_id IS NOT NULL
ORDER BY d.updated_at ASC
LIMIT ?;

-- name: SumRAGFlowDocumentsSizeByScope :one
-- 汇总知识库当前累计占用；失败/停止文件仍占用 RAGFlow 原文件存储，因此全部状态都计入。
SELECT CAST(COALESCE(SUM(size_bytes), 0) AS SIGNED) AS total_size_bytes
FROM ragflow_documents
WHERE scope_type = ?
  AND org_id = ?
  AND (sqlc.narg(app_id) IS NULL OR app_id = sqlc.narg(app_id));
