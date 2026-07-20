-- 平台提示词 rollout 是全局 singleton；guard 行供启动事务 SELECT ... FOR UPDATE 串行化检查和创建。
CREATE TABLE aicc_platform_prompt_rollout_guards (
    singleton TINYINT NOT NULL PRIMARY KEY,
    CONSTRAINT aicc_platform_prompt_rollout_guards_singleton_check CHECK (singleton = 1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

INSERT INTO aicc_platform_prompt_rollout_guards (singleton) VALUES (1);

-- 新任务类型独立于企业模型 rollout，避免两类发布任务被同一 worker 语义混用。
ALTER TABLE jobs
    DROP CONSTRAINT jobs_type_check,
    ADD CONSTRAINT jobs_type_check CHECK (type IN (
        'app_initialize','app_start_container','app_stop_container','app_restart_container','app_delete',
        'channel_start_login','channel_check_binding','runtime_node_health_reconcile','runtime_refresh_status',
        'app_health_check','newapi_disable_key','newapi_restore_key','workspace_archive_cleanup',
        'web_publish_provision','aicc_model_rollout','aicc_platform_prompt_rollout'));
