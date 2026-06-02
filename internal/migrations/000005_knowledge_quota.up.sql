-- 知识库空间上限：所有企业与实例都必须有累计容量限制，默认 1GB。
-- 线上若存在历史空值，先统一补 1GB，再启用 NOT NULL 约束；新增列在 MySQL 侧默认填充 1GB。
ALTER TABLE organizations
    ADD COLUMN knowledge_quota_bytes BIGINT NOT NULL DEFAULT 1073741824,
    ADD CONSTRAINT organizations_knowledge_quota_bytes_check
        CHECK (knowledge_quota_bytes > 0);

ALTER TABLE apps
    ADD COLUMN knowledge_quota_bytes BIGINT NOT NULL DEFAULT 1073741824,
    ADD CONSTRAINT apps_knowledge_quota_bytes_check
        CHECK (knowledge_quota_bytes > 0);
