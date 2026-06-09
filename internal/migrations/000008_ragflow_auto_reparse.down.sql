-- 回滚自动重解析重试状态：先删索引再删列，保证可回退最近一次迁移。
ALTER TABLE ragflow_documents
    DROP INDEX idx_ragflow_documents_auto_reparse,
    DROP COLUMN auto_reparse_next_at,
    DROP COLUMN auto_reparse_attempts;
