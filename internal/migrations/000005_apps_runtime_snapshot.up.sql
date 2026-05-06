-- apps.runtime_snapshot_json 缓存最近一次 runtime_refresh_status 采样的容器运行指标。
-- 字段为 nullable jsonb：尚未采集 / 容器已删除 / agent 不可达时保持 NULL。
-- 列内结构由 service.AppRuntimeSnapshot 定义；DB 不做强校验，避免每次扩字段都要迁移。
ALTER TABLE apps
    ADD COLUMN runtime_snapshot_json jsonb NULL,
    ADD COLUMN runtime_snapshot_at   timestamptz NULL;
