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
