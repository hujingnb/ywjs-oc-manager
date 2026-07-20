-- name: CreateJob :exec
-- 通用任务入口允许创建迁移约束声明的类型，包括企业模型变更触发的 aicc_model_rollout。
INSERT INTO jobs (
    id,
    type,
    status,
    priority,
    run_after,
    max_attempts,
    payload_json
) VALUES (
    ?, ?, 'pending', ?, ?, ?, ?
);

-- name: GetJob :one
SELECT *
FROM jobs
WHERE id = ?;

-- name: GetAICCModelRolloutLeaderJob :one
-- 同企业 pending/running 任务共同参与稳定排序，旧任务失败恢复 pending 后仍不会被新任务抢占。
SELECT *
FROM jobs
WHERE type = 'aicc_model_rollout'
  AND status IN ('pending', 'running')
  AND payload_json->>'$.org_id' = sqlc.arg(org_id)
ORDER BY created_at ASC, id ASC
LIMIT 1;

-- name: HasActiveAICCPlatformPromptRolloutJob :one
-- 全局平台提示词任务只允许一个 pending/running job，避免多个启动副本重复重启客服。
SELECT EXISTS (
    SELECT 1
    FROM jobs
    WHERE type = 'aicc_platform_prompt_rollout'
      AND status IN ('pending', 'running')
);

-- name: HasOtherActiveAICCPlatformPromptRolloutJob :one
-- 成功前后继调度排除当前 running 旧任务，但仍阻止任何其它 pending/running 同类任务。
SELECT EXISTS (
    SELECT 1 FROM jobs
    WHERE type = 'aicc_platform_prompt_rollout'
      AND status IN ('pending', 'running')
      AND id <> ?
);

-- name: LockAICCPlatformPromptRolloutGuard :one
-- 事务先锁住唯一 guard 行，再判断活跃任务、落后客服并创建任务，消除多副本启动的 TOCTOU。
SELECT singleton
FROM aicc_platform_prompt_rollout_guards
WHERE singleton = 1
FOR UPDATE;

-- name: GetAICCPlatformPromptRolloutLeaderJob :one
-- pending/running 任务按创建时间和主键稳定选 leader，供 worker 在重试与多副本下保持顺序。
SELECT *
FROM jobs
WHERE type = 'aicc_platform_prompt_rollout'
  AND status IN ('pending', 'running')
ORDER BY created_at ASC, id ASC
LIMIT 1;

-- name: UpdateJobPayload :execrows
-- rollout 在外部副作用之间持久化专属恢复标记；仅允许当前 running 任务更新自身 payload。
UPDATE jobs
SET payload_json = ?, updated_at = now()
WHERE id = ? AND status = 'running'
  AND locked_by = sqlc.arg(locked_by)
  AND lease_token = sqlc.arg(lease_token);

-- name: DeferJob :execrows
-- 非 leader 任务释放 worker 槽并短延迟回队列；抵消本次领取增加的 attempts，不消耗业务重试额度。
UPDATE jobs
SET status = 'pending',
    run_after = ?,
    attempts = GREATEST(attempts - 1, 0),
    last_error = NULL,
    locked_by = NULL,
    locked_at = NULL,
    lease_token = NULL,
    updated_at = now()
WHERE id = ? AND status = 'running'
  AND locked_by = sqlc.arg(locked_by)
  AND lease_token = sqlc.arg(lease_token);

-- name: ListReadyJobs :many
SELECT *
FROM jobs
WHERE status = 'pending'
  AND run_after <= now()
ORDER BY priority DESC, created_at ASC
LIMIT ?;

-- name: LockJobForUpdate :one
SELECT *
FROM jobs
WHERE id = ?
FOR UPDATE;

-- name: MarkJobRunning :execrows
UPDATE jobs
SET
    status = 'running',
    locked_by = ?,
    locked_at = now(),
    lease_token = ?,
    attempts = attempts + 1,
    updated_at = now()
WHERE id = ? AND status = 'pending' AND attempts < max_attempts;

-- name: MarkJobSucceeded :execrows
UPDATE jobs
SET
    status = 'succeeded',
    finished_at = now(),
    locked_by = NULL,
    locked_at = NULL,
    lease_token = NULL,
    updated_at = now()
WHERE id = ? AND status = 'running'
  AND locked_by = sqlc.arg(locked_by)
  AND lease_token = sqlc.arg(lease_token);

-- name: MarkJobFailed :execrows
UPDATE jobs
SET
    status = 'failed',
    last_error = ?,
    finished_at = now(),
    locked_by = NULL,
    locked_at = NULL,
    lease_token = NULL,
    updated_at = now()
WHERE id = ? AND status = 'running'
  AND locked_by = sqlc.arg(locked_by)
  AND lease_token = sqlc.arg(lease_token);

-- name: RetryJob :execrows
UPDATE jobs
SET
    status = 'pending',
    run_after = ?,
    last_error = ?,
    locked_by = NULL,
    locked_at = NULL,
    lease_token = NULL,
    updated_at = now()
WHERE id = ? AND status = 'running'
  AND locked_by = sqlc.arg(locked_by)
  AND lease_token = sqlc.arg(lease_token);

-- name: RenewJobLease :execrows
-- handler 执行期间周期续租；随机 token 防止同 worker_id 的旧进程覆盖新 owner。
UPDATE jobs
SET locked_at = now(), updated_at = now()
WHERE id = ? AND status = 'running'
  AND locked_by = sqlc.arg(locked_by)
  AND lease_token = sqlc.arg(lease_token);

-- name: GetLatestAppInitJob :one
-- reaper 通过 payload_json->>'$.app_id' 查最近一份 app_initialize job。
-- 用 ORDER BY created_at DESC + LIMIT 1 取最新；不存在返回 sql.ErrNoRows。
SELECT *
FROM jobs
WHERE type = 'app_initialize'
  -- 调用方为保持 JSON 参数类型传入带双引号的 app_id；必须先解包再与 ->> 的文本结果比较，
  -- 否则已存在的初始化任务会被误判为不存在，造成并发重复初始化及非法状态转换。
  AND payload_json->>'$.app_id' = JSON_UNQUOTE(sqlc.arg(app_id))
ORDER BY created_at DESC
LIMIT 1;

-- name: RequeueJob :exec
-- reaper 把已 running / succeeded 的 job 重置为 pending。
-- locked_by / locked_at 一并清空避免被旧 worker 误识别为本机持有。
-- 注意：jobs 表无 started_at 列，仅清 locked_* / last_error / 状态。
UPDATE jobs
SET status = 'pending',
    locked_by = NULL,
    locked_at = NULL,
    lease_token = NULL,
    last_error = NULL,
    updated_at = now()
WHERE id = ?;

-- name: RequeueExpiredRunningJobs :execrows
-- manager 进程异常退出会遗留 status=running 的任务；超过调用方传入的租约阈值后处理为 pending 或 failed。
-- attempts 未耗尽的任务由 scheduler 重新投递；已耗尽的任务直接失败，不能因重启额外执行一次 handler。
-- 仅处理明确带有过期 locked_at 的记录，避免误碰历史异常空锁行；attempts 始终不回退。
UPDATE jobs
SET status = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'pending' END,
    run_after = CASE WHEN attempts >= max_attempts THEN run_after ELSE now() END,
    locked_by = NULL,
    locked_at = NULL,
    lease_token = NULL,
    last_error = CASE
        WHEN attempts >= max_attempts THEN 'worker lock expired; max attempts reached'
        ELSE 'worker lock expired; requeued after lease timeout'
    END,
    finished_at = CASE WHEN attempts >= max_attempts THEN now() ELSE finished_at END,
    updated_at = now()
WHERE status = 'running'
  AND locked_at < sqlc.arg(locked_before);
