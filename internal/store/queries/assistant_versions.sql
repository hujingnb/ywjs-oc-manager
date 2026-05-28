-- name: CreateAssistantVersion :exec
INSERT INTO assistant_versions (
    id, name, description, system_prompt, image_id, main_model,
    routing_json, skills_json, created_by
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?
);

-- name: GetAssistantVersion :one
SELECT * FROM assistant_versions
WHERE id = ? AND deleted_at IS NULL;

-- name: GetAssistantVersionByName :one
SELECT * FROM assistant_versions
WHERE name = ? AND deleted_at IS NULL;

-- name: ListAssistantVersions :many
SELECT * FROM assistant_versions
WHERE deleted_at IS NULL
ORDER BY created_at DESC, id DESC;

-- name: UpdateAssistantVersion :exec
-- revision 由 service 计算后整体写入（仅容器相关字段变更才递增）。
UPDATE assistant_versions
SET name = ?,
    description = ?,
    system_prompt = ?,
    image_id = ?,
    main_model = ?,
    routing_json = ?,
    skills_json = ?,
    revision = ?,
    updated_at = now()
WHERE id = ? AND deleted_at IS NULL;

-- name: UpdateAssistantVersionSkills :exec
-- skill 上传/删除单独走此查询：只改 skills_json 与 revision，避免覆盖其它字段。
UPDATE assistant_versions
SET skills_json = ?,
    revision = ?,
    updated_at = now()
WHERE id = ? AND deleted_at IS NULL;

-- name: SoftDeleteAssistantVersion :exec
UPDATE assistant_versions
SET deleted_at = now(), updated_at = now()
WHERE id = ? AND deleted_at IS NULL;

-- name: CountAppsUsingVersion :one
-- 严格保护：版本被未删除实例引用时不可删除。
SELECT count(*) FROM apps
WHERE version_id = ? AND deleted_at IS NULL;

-- name: CountOrgsUsingVersion :one
-- 严格保护：版本出现在任意未删除组织 allowlist 时不可删除。
SELECT count(*) FROM organizations
WHERE deleted_at IS NULL AND JSON_CONTAINS(assistant_version_ids, JSON_QUOTE(?));
