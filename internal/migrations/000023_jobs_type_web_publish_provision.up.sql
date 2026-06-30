-- 放宽 jobs.type CHECK 约束，新增 'web_publish_provision'（web-publish 企业开通 provisioning job 类型）。
-- 000021/000022 引入 web-publish 能力时只建了 org_web_publish_config / published_sites 表，
-- 遗漏扩展 jobs_type_check：平台管理员点「开通 Web 发布」会入队 web_publish_provision job，
-- 触发 Error 3819 Check constraint 'jobs_type_check' is violated 而 500。本迁移补齐该取值。
ALTER TABLE jobs
    DROP CONSTRAINT jobs_type_check,
    ADD CONSTRAINT jobs_type_check CHECK (type IN (
        'app_initialize','app_start_container','app_stop_container','app_restart_container','app_delete',
        'channel_start_login','channel_check_binding','runtime_node_health_reconcile','runtime_refresh_status',
        'app_health_check','newapi_disable_key','newapi_restore_key','workspace_archive_cleanup',
        'web_publish_provision'));
