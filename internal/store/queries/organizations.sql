-- name: CreateOrganization :one
INSERT INTO organizations (
    name,
    status,
    contact_name,
    contact_phone,
    remark,
    credit_warning_threshold
) VALUES (
    $1, $2, $3, $4, $5, $6
)
RETURNING *;

-- name: SetOrganizationNewAPIUser :one
UPDATE organizations
SET
    newapi_user_id = $2,
    newapi_user_credentials_ciphertext = $3,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: GetOrganization :one
SELECT *
FROM organizations
WHERE id = $1;

-- name: GetOrganizationByName :one
SELECT *
FROM organizations
WHERE name = $1;

-- name: ListOrganizations :many
SELECT *
FROM organizations
WHERE deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT $1 OFFSET $2;

-- name: UpdateOrganizationProfile :one
UPDATE organizations
SET
    name = $2,
    contact_name = $3,
    contact_phone = $4,
    remark = $5,
    credit_warning_threshold = $6,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetOrganizationStatus :one
UPDATE organizations
SET status = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SoftDeleteOrganization :one
UPDATE organizations
SET status = 'deleted', deleted_at = now(), updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: HardDeleteOrganization :exec
-- 用于组织创建链路失败时回滚刚刚 INSERT 的孤儿记录。
-- 正常生命周期不可见此查询；普通"删除"必须走 SoftDeleteOrganization。
DELETE FROM organizations WHERE id = $1;
