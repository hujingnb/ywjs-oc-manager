-- name: ListAppSkillsByApp :many
SELECT * FROM app_skills WHERE app_id = ? ORDER BY name ASC;

-- name: GetAppSkillByAppAndName :one
SELECT * FROM app_skills WHERE app_id = ? AND name = ?;

-- name: CreateAppSkill :exec
INSERT INTO app_skills (
    id, app_id, name, source, source_ref, version, latest_version,
    cached_tar_path, source_metadata, file_size, file_sha256, installed_by
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: DeleteAppSkillByAppAndName :exec
DELETE FROM app_skills WHERE app_id = ? AND name = ?;

-- name: UpdateAppSkillVersion :exec
UPDATE app_skills SET version = ?, cached_tar_path = ?, file_size = ?, file_sha256 = ?,
    source_metadata = ?, latest_version = NULL WHERE app_id = ? AND name = ?;

-- name: UpdateAppSkillLatest :exec
UPDATE app_skills SET latest_version = ?, last_checked_at = now() WHERE id = ?;

-- name: ListDistinctAppSkillSources :many
SELECT DISTINCT source, source_ref FROM app_skills;

-- name: ListAppSkillsBySourceRef :many
SELECT * FROM app_skills WHERE source = ? AND source_ref = ?;
