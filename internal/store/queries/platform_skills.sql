-- name: CreatePlatformSkill :exec
INSERT INTO platform_skills (
    id, name, description, version, tar_path, file_size, file_sha256, metadata_json, uploaded_by
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetPlatformSkill :one
SELECT * FROM platform_skills WHERE id = ?;

-- name: GetPlatformSkillByNameVersion :one
SELECT * FROM platform_skills WHERE name = ? AND version = ?;

-- name: ListPlatformSkills :many
SELECT * FROM platform_skills ORDER BY name ASC, created_at DESC, id DESC;

-- name: DeletePlatformSkill :exec
DELETE FROM platform_skills WHERE id = ?;
