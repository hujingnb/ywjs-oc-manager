-- 还原唯一约束到单列（注意：若已有 feishu 绑定行，回滚会因约束冲突失败，属预期）。
ALTER TABLE channel_bindings
    DROP INDEX uk_channel_bindings_app_active,
    ADD UNIQUE KEY uk_channel_bindings_app_active (app_active_key);

-- 还原 CHECK 约束至仅允许 wechat（如有 feishu 数据行，此处亦会失败，属预期）。
ALTER TABLE channel_bindings
    DROP CONSTRAINT channel_bindings_channel_type_check,
    ADD CONSTRAINT channel_bindings_channel_type_check CHECK (channel_type IN ('wechat'));
