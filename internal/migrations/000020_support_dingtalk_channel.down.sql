-- 回滚：移除 'dingtalk'，还原为飞书+企业微信三值约束。
ALTER TABLE channel_bindings
    DROP CONSTRAINT channel_bindings_channel_type_check,
    ADD CONSTRAINT channel_bindings_channel_type_check CHECK (channel_type IN ('wechat', 'feishu', 'work_wechat'));
