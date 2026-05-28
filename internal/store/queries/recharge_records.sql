-- name: CreateRechargeRecord :exec
INSERT INTO recharge_records (
    id,
    org_id,
    operator_id,
    credit_amount,
    remark,
    newapi_ref_id,
    status,
    error_message
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?
);

-- name: GetRechargeRecord :one
SELECT *
FROM recharge_records
WHERE id = ?;

-- name: ListRechargeRecordsByOrg :many
SELECT *
FROM recharge_records
WHERE org_id = ?
ORDER BY created_at DESC, id DESC
LIMIT ? OFFSET ?;

-- name: SumRechargeAmountByOrg :one
-- SumRechargeAmountByOrg 聚合指定组织所有成功充值记录的总额。
-- 仅统计 status='succeeded' 的记录，failed 记录不计入累计金额。
SELECT COALESCE(SUM(credit_amount), 0) AS total_recharged
FROM recharge_records
WHERE org_id = ? AND status = 'succeeded';
