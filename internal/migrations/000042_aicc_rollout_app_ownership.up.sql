-- 同一 AICC app 的模型 rollout 与平台提示词 rollout 共享唯一 ownership，防止跨任务重启与 Ready 收口交错。
CREATE TABLE aicc_rollout_app_owners (
    app_id CHAR(36) PRIMARY KEY,
    owner_job_id CHAR(36) NOT NULL,
    owner_job_type VARCHAR(64) NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    CONSTRAINT fk_aicc_rollout_app_owners_app FOREIGN KEY (app_id) REFERENCES apps(id) ON DELETE CASCADE,
    CONSTRAINT aicc_rollout_app_owners_job_type_check CHECK (
        owner_job_type IN ('aicc_model_rollout', 'aicc_platform_prompt_rollout')
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
