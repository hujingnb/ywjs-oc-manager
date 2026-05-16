-- apps 表:扩展 status CHECK 约束、新增通用进度字段与上次错误状态字段。
-- status 由 5 个 init 子状态替换原 'initializing',存量行就地迁移。
-- progress_current / progress_total / last_error_status 设计为通用字段,
-- 不绑死 init 段:未来重启容器、停止等待优雅退出等长耗时操作都可复用。

ALTER TABLE apps DROP CONSTRAINT apps_status_check;

UPDATE apps SET status = 'pulling_image' WHERE status = 'initializing';

ALTER TABLE apps ADD CONSTRAINT apps_status_check CHECK (
    status IN (
        'draft',
        'pulling_image', 'syncing_image', 'preparing_runtime',
        'creating_container', 'starting',
        'binding_waiting', 'binding_failed',
        'running', 'stopped', 'error', 'deleted'
    )
);

ALTER TABLE apps
    ADD COLUMN progress_current bigint NULL,
    ADD COLUMN progress_total bigint NULL,
    ADD COLUMN last_error_status text NULL;

COMMENT ON COLUMN apps.progress_current IS '当前 status 对应阶段的已完成量;语义随 status 变化(字节 / 秒 / count),不可知时为 NULL。';
COMMENT ON COLUMN apps.progress_total IS '当前 status 对应阶段的总量;不可知时为 NULL(前端展示为不定进度)。';
COMMENT ON COLUMN apps.last_error_status IS '上次进入 error 时所在的状态值;进入 error 时写入,重新发起对应转移时清空。不加 CHECK,靠应用层在写入时校验。';
