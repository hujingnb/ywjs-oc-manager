-- 定制技能相关查询。本文件首版仅放工单附件三条查询；
-- 后续 Task 3 再追加 custom_skills / custom_skill_targets / 交付相关查询。

-- name: CreateSkillTicketAttachment :exec
INSERT INTO skill_ticket_attachments (id, ticket_id, comment_id, object_path, file_name, file_size, uploaded_by)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListSkillTicketAttachments :many
SELECT * FROM skill_ticket_attachments WHERE ticket_id = ? ORDER BY created_at ASC, id ASC;

-- name: GetSkillTicketAttachment :one
SELECT * FROM skill_ticket_attachments WHERE id = ?;
