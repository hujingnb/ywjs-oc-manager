-- 企业实例数量上限：max_instance_count 为 NULL 表示不限制，正整数为上限。
-- 「实例」指 apps 表中 deleted_at IS NULL 的应用；上限校验在 service 层完成
-- （事务内 COUNT 现存未删除实例数），本列仅持久化上限值。存量企业默认 NULL，不影响现有行为。
ALTER TABLE organizations
    ADD COLUMN max_instance_count INT NULL,
    ADD CONSTRAINT organizations_max_instance_count_check
        CHECK (max_instance_count IS NULL OR max_instance_count > 0);
