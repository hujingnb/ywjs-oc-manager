-- 回滚企业级个人知识库默认配额：先删 CHECK 约束再删列（MySQL 8 支持 DROP CONSTRAINT）。
ALTER TABLE organizations
    DROP CONSTRAINT organizations_default_app_kb_quota_check,
    DROP COLUMN default_app_knowledge_quota_bytes;
