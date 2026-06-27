-- 企业微信渠道：在 wechat+feishu 基础上放宽 channel_type CHECK 至再加 work_wechat。
-- 唯一约束 uk_channel_bindings_app_active 已由 000015 改为 (app_active_key, channel_type)，
-- 企业微信直接受益（同一 app 可 wechat/feishu/work_wechat 各一条非 deleted 绑定），此处不动。
ALTER TABLE channel_bindings
    DROP CONSTRAINT channel_bindings_channel_type_check,
    ADD CONSTRAINT channel_bindings_channel_type_check CHECK (channel_type IN ('wechat', 'feishu', 'work_wechat'));
