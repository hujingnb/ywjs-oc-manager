-- 回滚企业实例数量上限：先删 CHECK 约束再删列（MySQL 8 支持 DROP CONSTRAINT）。
ALTER TABLE organizations
    DROP CONSTRAINT organizations_max_instance_count_check,
    DROP COLUMN max_instance_count;
