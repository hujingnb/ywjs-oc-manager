-- name: GetCurrentOrganizationPersona :one
SELECT *
FROM organization_personas
WHERE org_id = $1
ORDER BY version DESC
LIMIT 1;

-- name: ListOrganizationPersonaVersions :many
SELECT *
FROM organization_personas
WHERE org_id = $1
ORDER BY version DESC
LIMIT $2 OFFSET $3;

-- name: CreateOrganizationPersona :one
INSERT INTO organization_personas (
    org_id,
    system_prompt,
    conversation_rules,
    forbidden_rules,
    reply_style,
    allow_member_override,
    version,
    created_by
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING *;
