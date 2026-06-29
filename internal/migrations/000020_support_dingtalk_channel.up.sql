-- 放宽 channel_bindings.channel_type CHECK 约束，新增 'dingtalk'。
-- 唯一约束 uk_channel_bindings_app_active (app_active_key, channel_type) 由飞书 000015 已建，
-- 含 channel_type，钉钉直接受益（同一 app 可同时绑定 wechat/feishu/work_wechat/dingtalk 各一条非 deleted）。
ALTER TABLE channel_bindings
    DROP CONSTRAINT channel_bindings_channel_type_check,
    ADD CONSTRAINT channel_bindings_channel_type_check CHECK (channel_type IN ('wechat', 'feishu', 'work_wechat', 'dingtalk'));
