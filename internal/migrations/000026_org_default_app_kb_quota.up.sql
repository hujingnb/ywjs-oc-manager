-- 企业级「个人知识库空间」默认配额：作为该企业新建实例（成员个人知识库）的默认知识库容量上限。
-- 默认 1GB，与实例创建历史默认值一致，存量企业行为不变；不影响已有实例，实例仍可单独调整。
ALTER TABLE organizations
    ADD COLUMN default_app_knowledge_quota_bytes BIGINT NOT NULL DEFAULT 1073741824
        COMMENT '该企业新建实例的默认个人知识库空间上限（字节），默认 1GB',
    ADD CONSTRAINT organizations_default_app_kb_quota_check
        CHECK (default_app_knowledge_quota_bytes > 0);
