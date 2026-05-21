-- name: CreateAssistantVersion :one
INSERT INTO assistant_versions (
    name, description, system_prompt, image_id, main_model,
    routing_json, skills_json, created_by
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING *;

-- name: GetAssistantVersion :one
SELECT * FROM assistant_versions
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetAssistantVersionByName :one
SELECT * FROM assistant_versions
WHERE name = $1 AND deleted_at IS NULL;

-- name: ListAssistantVersions :many
SELECT * FROM assistant_versions
WHERE deleted_at IS NULL
ORDER BY created_at DESC, id DESC;

-- name: UpdateAssistantVersion :one
-- revision 由 service 计算后整体写入（仅容器相关字段变更才递增）。
UPDATE assistant_versions
SET name = $2,
    description = $3,
    system_prompt = $4,
    image_id = $5,
    main_model = $6,
    routing_json = $7,
    skills_json = $8,
    revision = $9,
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: UpdateAssistantVersionSkills :one
-- skill 上传/删除单独走此查询：只改 skills_json 与 revision，避免覆盖其它字段。
UPDATE assistant_versions
SET skills_json = $2,
    revision = $3,
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteAssistantVersion :one
UPDATE assistant_versions
SET deleted_at = now(), updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: CountAppsUsingVersion :one
-- 严格保护：版本被未删除实例引用时不可删除。
SELECT count(*) FROM apps
WHERE version_id = $1 AND deleted_at IS NULL;

-- name: CountOrgsUsingVersion :one
-- 严格保护：版本出现在任意未删除组织 allowlist 时不可删除。
SELECT count(*) FROM organizations
WHERE deleted_at IS NULL AND jsonb_exists(assistant_version_ids, $1);
