-- name: CreateJob :one
INSERT INTO jobs (
    type,
    status,
    priority,
    run_after,
    max_attempts,
    payload_json
) VALUES (
    $1, 'pending', $2, $3, $4, $5
)
RETURNING *;

-- name: GetJob :one
SELECT *
FROM jobs
WHERE id = $1;

-- name: ListReadyJobs :many
SELECT *
FROM jobs
WHERE status = 'pending'
  AND run_after <= now()
ORDER BY priority DESC, created_at ASC
LIMIT $1;

-- name: LockJobForUpdate :one
SELECT *
FROM jobs
WHERE id = $1
FOR UPDATE;

-- name: MarkJobRunning :one
UPDATE jobs
SET
    status = 'running',
    locked_by = $2,
    locked_at = now(),
    attempts = attempts + 1,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkJobSucceeded :one
UPDATE jobs
SET
    status = 'succeeded',
    finished_at = now(),
    locked_by = NULL,
    locked_at = NULL,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkJobFailed :one
UPDATE jobs
SET
    status = 'failed',
    last_error = $2,
    finished_at = now(),
    locked_by = NULL,
    locked_at = NULL,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: RetryJob :one
UPDATE jobs
SET
    status = 'pending',
    run_after = $2,
    last_error = $3,
    locked_by = NULL,
    locked_at = NULL,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: GetLatestAppInitJob :one
-- reaper 通过 payload_json->>'app_id' 查最近一份 app_initialize job。
-- 用 ORDER BY created_at DESC + LIMIT 1 取最新；不存在返回 pgx.ErrNoRows。
-- 参数显式 cast 成 text，避免 sqlc 把 `->>` 结果类型推断成 []byte。
SELECT *
FROM jobs
WHERE type = 'app_initialize'
  AND payload_json->>'app_id' = sqlc.arg('app_id')::text
ORDER BY created_at DESC
LIMIT 1;

-- name: RequeueJob :one
-- reaper 把已 running / succeeded 的 job 重置为 pending。
-- locked_by / locked_at 一并清空避免被旧 worker 误识别为本机持有。
-- 注意：jobs 表无 started_at 列，仅清 locked_* / last_error / 状态。
UPDATE jobs
SET status = 'pending',
    locked_by = NULL,
    locked_at = NULL,
    last_error = NULL,
    updated_at = now()
WHERE id = $1
RETURNING *;
