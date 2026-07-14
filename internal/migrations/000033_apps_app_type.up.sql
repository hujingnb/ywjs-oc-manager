-- 用枚举化应用类型替代布尔隐藏标记，便于后续在不复用语义的前提下扩展应用类别。
ALTER TABLE apps
    ADD COLUMN app_type VARCHAR(32) NOT NULL DEFAULT 'standard' COMMENT '应用类型：standard=普通应用，aicc=AICC 应用';

-- 先回填存量客服应用，再切换唯一约束，避免迁移期间 AICC 应用错误占用普通应用名额。
UPDATE apps SET app_type = CASE WHEN aicc_hidden THEN 'aicc' ELSE 'standard' END;

-- MySQL 以生成列上的唯一索引模拟部分唯一索引：仅 active standard 应用限制每个 owner 一条。
ALTER TABLE apps
    DROP INDEX uk_apps_owner_active,
    MODIFY COLUMN owner_active_key CHAR(36)
        GENERATED ALWAYS AS (
            CASE WHEN deleted_at IS NULL AND app_type = 'standard' THEN owner_user_id END
        ) VIRTUAL,
    ADD UNIQUE KEY uk_apps_owner_active (owner_active_key),
    ADD CONSTRAINT apps_app_type_check CHECK (app_type IN ('standard', 'aicc')),
    DROP COLUMN aicc_hidden;
