-- 飞书渠道：放宽 channel_type CHECK 至 wechat+feishu，并把唯一约束加上 channel_type，
-- 让同一 app 的 wechat 与 feishu 各保留一条非 deleted 绑定（渠道并存）。
ALTER TABLE channel_bindings
    DROP CONSTRAINT channel_bindings_channel_type_check,
    ADD CONSTRAINT channel_bindings_channel_type_check CHECK (channel_type IN ('wechat', 'feishu'));

-- 唯一约束由 (app_active_key) 改为 (app_active_key, channel_type)：同一 app 多渠道并存。
-- app_active_key 为 VIRTUAL 生成列：非 deleted 行值为 app_id，deleted 行为 NULL。
-- MySQL UNIQUE 对含 NULL 的复合列不检查重复，deleted 行可无限共存；
-- 非 deleted 行 (app_id, channel_type) 组合唯一，确保每渠道至多一条活跃绑定。
ALTER TABLE channel_bindings
    DROP INDEX uk_channel_bindings_app_active,
    ADD UNIQUE KEY uk_channel_bindings_app_active (app_active_key, channel_type);
