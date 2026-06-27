-- 还原 CHECK 约束至 wechat+feishu（如已有 work_wechat 数据行，回滚会因约束冲突失败，属预期）。
ALTER TABLE channel_bindings
    DROP CONSTRAINT channel_bindings_channel_type_check,
    ADD CONSTRAINT channel_bindings_channel_type_check CHECK (channel_type IN ('wechat', 'feishu'));
