-- name: CreateOrganization :exec
INSERT INTO organizations (
    id,
    name,
    code,
    status,
    contact_name,
    contact_phone,
    remark,
    credit_warning_threshold,
    max_instance_count,
    knowledge_quota_bytes,
    assistant_version_ids
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?,
    COALESCE(NULLIF(CAST(sqlc.arg(knowledge_quota_bytes) AS SIGNED), 0), 1073741824),
    ?
);

-- name: SetOrganizationNewAPIUser :exec
UPDATE organizations
SET
    newapi_user_id = ?,
    newapi_user_credentials_ciphertext = ?,
    newapi_username = ?,
    updated_at = now()
WHERE id = ?;

-- name: GetOrganization :one
SELECT *
FROM organizations
WHERE id = ?;

-- name: GetOrganizationByName :one
SELECT *
FROM organizations
WHERE name = ?;

-- name: GetOrganizationByCode :one
SELECT *
FROM organizations
WHERE code = ?;

-- name: ListOrganizations :many
SELECT *
FROM organizations
WHERE deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT ? OFFSET ?;

-- name: UpdateOrganizationProfile :exec
UPDATE organizations
SET
    name = ?,
    contact_name = ?,
    contact_phone = ?,
    remark = ?,
    credit_warning_threshold = ?,
    max_instance_count = ?,
    knowledge_quota_bytes = COALESCE(NULLIF(CAST(sqlc.arg(knowledge_quota_bytes) AS SIGNED), 0), knowledge_quota_bytes),
    assistant_version_ids = ?,
    updated_at = now()
WHERE id = ?;

-- name: SetOrganizationStatus :exec
UPDATE organizations
SET status = ?, updated_at = now()
WHERE id = ?;

-- name: SoftDeleteOrganization :exec
UPDATE organizations
SET status = 'deleted', deleted_at = now(), updated_at = now()
WHERE id = ? AND deleted_at IS NULL;

-- name: HardDeleteOrganization :exec
-- 用于组织创建链路失败时回滚刚刚 INSERT 的孤儿记录。
-- 正常生命周期不可见此查询；普通"删除"必须走 SoftDeleteOrganization。
DELETE FROM organizations WHERE id = ?;

-- name: GetOrganizationForUpdate :one
-- OOS-2 access_token 自愈用：以行锁查询组织，避免并发更新密文时出现写丢失。
SELECT *
FROM organizations
WHERE id = ?
FOR UPDATE;

-- name: UpdateOrganizationCredentialsCiphertext :exec
-- OOS-2 access_token 自愈用：仅更新 newapi_user_credentials_ciphertext，不动 newapi_user_id。
UPDATE organizations
SET newapi_user_credentials_ciphertext = ?,
    updated_at = now()
WHERE id = ?;

-- name: ListAllActiveOrganizations :many
-- 全量返回活跃组织（deleted_at IS NULL），不分页；
-- 仅供平台内部聚合使用（如 GetOrgUsageBreakdown），请勿用于用户可见的列表接口。
SELECT *
FROM organizations
WHERE deleted_at IS NULL
ORDER BY created_at DESC, id DESC;
