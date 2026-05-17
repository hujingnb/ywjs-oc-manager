-- name: CreateRechargeRecord :one
INSERT INTO recharge_records (
    org_id,
    operator_id,
    credit_amount,
    remark,
    newapi_ref_id,
    status,
    error_message
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING *;

-- name: ListRechargeRecordsByOrg :many
SELECT *
FROM recharge_records
WHERE org_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2 OFFSET $3;

-- name: SumRechargeAmountByOrg :one
-- SumRechargeAmountByOrg 聚合指定组织所有成功充值记录的总额。
-- 仅统计 status='succeeded' 的记录，failed 记录不计入累计金额。
SELECT COALESCE(SUM(credit_amount), 0)::bigint AS total_recharged
FROM recharge_records
WHERE org_id = $1 AND status = 'succeeded';
