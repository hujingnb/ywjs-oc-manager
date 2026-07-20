-- 旧版本无法消费平台提示词 rollout，先删除该类任务再收紧 jobs 类型约束。
DELETE FROM jobs WHERE type = 'aicc_platform_prompt_rollout';

ALTER TABLE jobs
    DROP CONSTRAINT jobs_type_check,
    ADD CONSTRAINT jobs_type_check CHECK (type IN (
        'app_initialize','app_start_container','app_stop_container','app_restart_container','app_delete',
        'channel_start_login','channel_check_binding','runtime_node_health_reconcile','runtime_refresh_status',
        'app_health_check','newapi_disable_key','newapi_restore_key','workspace_archive_cleanup',
        'web_publish_provision','aicc_model_rollout'));

DROP TABLE aicc_platform_prompt_rollout_guards;
