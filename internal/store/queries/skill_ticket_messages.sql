-- name: CreateSkillTicketMessage :exec
INSERT INTO skill_ticket_messages (id, ticket_id, author_user_id, kind, body) VALUES (?, ?, ?, ?, ?);

-- name: ListSkillTicketMessages :many
SELECT * FROM skill_ticket_messages WHERE ticket_id = ? ORDER BY created_at ASC, id ASC;

-- name: GetSkillTicketMessage :one
SELECT * FROM skill_ticket_messages WHERE id = ?;
