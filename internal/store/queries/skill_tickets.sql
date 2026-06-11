-- name: CreateSkillTicket :exec
INSERT INTO skill_tickets (
    id, org_id, requester_user_id, requester_role, title, status
) VALUES (?, ?, ?, ?, ?, ?);

-- name: GetSkillTicket :one
SELECT * FROM skill_tickets WHERE id = ?;

-- name: ListSkillTicketsByRequester :many
SELECT * FROM skill_tickets WHERE requester_user_id = ? ORDER BY updated_at DESC, id DESC;

-- name: ListAllSkillTickets :many
SELECT * FROM skill_tickets ORDER BY (status = 'pending') DESC, updated_at DESC, id DESC;

-- name: UpdateSkillTicketStatus :exec
UPDATE skill_tickets SET status = ? WHERE id = ?;

-- name: SetSkillTicketQuote :exec
UPDATE skill_tickets SET quote_amount_cents = ? WHERE id = ?;

-- name: RejectSkillTicket :exec
UPDATE skill_tickets SET status = 'rejected', reject_reason = ? WHERE id = ?;

-- name: TouchSkillTicket :exec
UPDATE skill_tickets SET updated_at = CURRENT_TIMESTAMP(6) WHERE id = ?;

-- name: CountPendingSkillTickets :one
SELECT COUNT(*) FROM skill_tickets WHERE status = 'pending';
