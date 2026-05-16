-- apps 表：扩展 status CHECK 约束（增加 pulling_runtime_image），
-- 新增 runtime_image_ref 和 runtime_image_sha256 两列。
-- 存量行两列默认空串，等首次重新初始化时由 phasePullRuntimeImage 写入。

ALTER TABLE apps DROP CONSTRAINT apps_status_check;

ALTER TABLE apps ADD CONSTRAINT apps_status_check CHECK (
    status IN (
        'draft',
        'pulling_runtime_image',
        'pulling_image', 'syncing_image', 'preparing_runtime',
        'creating_container', 'starting',
        'binding_waiting', 'binding_failed',
        'running', 'stopped', 'error', 'deleted'
    )
);

ALTER TABLE apps
    ADD COLUMN runtime_image_ref    TEXT NOT NULL DEFAULT '',
    ADD COLUMN runtime_image_sha256 TEXT NOT NULL DEFAULT '';

COMMENT ON COLUMN apps.runtime_image_ref    IS '部署时实际使用的镜像引用（含 tag）；phasePullRuntimeImage 写入，之后不变。';
COMMENT ON COLUMN apps.runtime_image_sha256 IS '拉取后 docker inspect 返回的镜像 ID（sha256:…）；供展示和排查使用。';
