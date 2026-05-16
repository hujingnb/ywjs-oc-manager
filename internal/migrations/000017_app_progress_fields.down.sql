-- 反向迁移:把 5 个 init 子状态合并回 'initializing',删进度字段与 status CHECK 调整。
-- 注意:如果 down 时 apps.status 已经处于 binding_waiting / running 等下游状态,
-- 这些行不受影响;只有 5 个 init 子状态行被合并。

ALTER TABLE apps DROP CONSTRAINT apps_status_check;

UPDATE apps SET status = 'initializing'
WHERE status IN ('pulling_image','syncing_image','preparing_runtime','creating_container','starting');

ALTER TABLE apps ADD CONSTRAINT apps_status_check CHECK (
    status IN (
        'draft', 'initializing',
        'binding_waiting', 'binding_failed',
        'running', 'stopped', 'error', 'deleted'
    )
);

ALTER TABLE apps
    DROP COLUMN progress_current,
    DROP COLUMN progress_total,
    DROP COLUMN last_error_status;
