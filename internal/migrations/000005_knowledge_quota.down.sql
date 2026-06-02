-- 回滚知识库空间上限：先删 CHECK 约束再删列（MySQL 8 支持 DROP CONSTRAINT）。
ALTER TABLE apps
    DROP CONSTRAINT apps_knowledge_quota_bytes_check,
    DROP COLUMN knowledge_quota_bytes;

ALTER TABLE organizations
    DROP CONSTRAINT organizations_knowledge_quota_bytes_check,
    DROP COLUMN knowledge_quota_bytes;
