-- apps.restart_policy_json 控制 app_health_check handler 在容器 unhealthy 时是否触发自动重启。
-- 字段为 jsonb，默认 mode=on_failure / max_per_window=5 / window_seconds=600；
-- 模式枚举 none / on_failure / always 由 service 层校验，DB 不强制约束便于 schema 演进。
ALTER TABLE apps
    ADD COLUMN restart_policy_json jsonb NOT NULL DEFAULT '{"mode":"on_failure","max_per_window":5,"window_seconds":600}'::jsonb,
    ADD COLUMN health_state_json   jsonb NULL;
