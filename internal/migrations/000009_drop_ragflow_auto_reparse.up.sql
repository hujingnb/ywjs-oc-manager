-- 删除自动重解析两列与索引：自动重试逻辑统一收口到新的「异常自愈定时任务」，
-- 重试次数/冷却/给上一律放 Redis（瞬时、自动过期），不再持久化到 DB，避免列与新方案语义重叠。
-- 先删索引再删列，保证可回退最近一次迁移。
ALTER TABLE ragflow_documents
    DROP INDEX idx_ragflow_documents_auto_reparse,
    DROP COLUMN auto_reparse_next_at,
    DROP COLUMN auto_reparse_attempts;
