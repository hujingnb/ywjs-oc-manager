-- 定制技能相关查询。工单附件三条查询已在 000011 迁移后移除（改用统一消息表 skill_ticket_messages）；
-- 本文件保留 custom_skills / custom_skill_targets / 交付相关查询。

-- name: CreateCustomSkill :exec
INSERT INTO custom_skills (id, name, description, version, tar_path, file_size, file_sha256, ticket_id, created_by)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetCustomSkillByNameVersion :one
SELECT * FROM custom_skills WHERE name = ? AND version = ?;

-- name: GetLatestCustomSkillByName :one
SELECT * FROM custom_skills WHERE name = ? ORDER BY created_at DESC, id DESC LIMIT 1;

-- name: ListCustomSkillVersionsByName :many
SELECT * FROM custom_skills WHERE name = ? ORDER BY created_at DESC, id DESC;

-- name: ListAllCustomSkills :many
SELECT * FROM custom_skills ORDER BY name ASC, created_at DESC, id DESC;

-- name: CreateCustomSkillTarget :exec
INSERT INTO custom_skill_targets (id, custom_skill_name, org_id, audience) VALUES (?, ?, ?, ?);

-- name: DeleteCustomSkillTargetsByName :exec
DELETE FROM custom_skill_targets WHERE custom_skill_name = ?;

-- name: ListCustomSkillTargetsByName :many
SELECT * FROM custom_skill_targets WHERE custom_skill_name = ? ORDER BY org_id ASC;

-- name: MarkSkillTicketDelivered :exec
UPDATE skill_tickets SET status = 'delivered', custom_skill_name = ? WHERE id = ?;

-- 市场可见性：返回对某主体(org_id + 是否管理员 + user_id)可见的定制技能(含申请人名与命中受众)。
-- 同名多行(多版本)由 service 取首条(created_at DESC)为最新。
-- name: ListVisibleCustomSkills :many
SELECT cs.id, cs.name, cs.description, cs.version, cs.tar_path, cs.file_size, cs.file_sha256,
       cs.ticket_id, cs.created_by, cs.created_at,
       tk.requester_user_id AS requester_user_id,
       u.username AS requester_username,
       t.audience AS audience
FROM custom_skills cs
JOIN skill_tickets tk ON tk.id = cs.ticket_id
JOIN users u ON u.id = tk.requester_user_id
JOIN custom_skill_targets t ON t.custom_skill_name = cs.name
WHERE t.org_id = sqlc.arg(org_id)
  AND (
        t.audience = 'all_org'
        OR (t.audience = 'org_admins' AND sqlc.arg(is_admin) = 1)
        OR (t.audience = 'requester_only' AND tk.requester_user_id = sqlc.arg(user_id))
      )
ORDER BY cs.name ASC, cs.created_at DESC, cs.id DESC;
