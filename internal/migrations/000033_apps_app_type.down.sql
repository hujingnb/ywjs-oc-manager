-- 恢复旧代码依赖的布尔标记。未知 app_type 产生 NULL，因列 NOT NULL 使回滚失败，避免被静默当作普通应用。
ALTER TABLE apps
    ADD COLUMN aicc_hidden BOOLEAN NOT NULL DEFAULT FALSE COMMENT '是否为 AICC 自动创建的隐藏 app';

UPDATE apps
SET aicc_hidden = CASE
    WHEN app_type = 'aicc' THEN TRUE
    WHEN app_type = 'standard' THEN FALSE
    ELSE NULL
END;

-- 恢复旧的部分唯一语义：未删除且未隐藏的应用限制每个 owner 一条。
ALTER TABLE apps
    DROP INDEX uk_apps_owner_active,
    MODIFY COLUMN owner_active_key CHAR(36)
        GENERATED ALWAYS AS (
            CASE WHEN deleted_at IS NULL AND aicc_hidden = FALSE THEN owner_user_id END
        ) VIRTUAL,
    ADD UNIQUE KEY uk_apps_owner_active (owner_active_key),
    DROP CHECK apps_app_type_check,
    DROP COLUMN app_type;
