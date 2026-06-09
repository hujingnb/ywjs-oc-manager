-- 回滚：重建自动重解析两列与索引，列与索引定义与 000008 保持一致，便于本地回退最近一次迁移。
-- 仅重建结构、不回填数据：原存量回填的「立即可重试」语义已不再适用于新自愈方案。
ALTER TABLE ragflow_documents
    ADD COLUMN auto_reparse_attempts INT NOT NULL DEFAULT 0 AFTER last_error,
    ADD COLUMN auto_reparse_next_at DATETIME(6) NULL AFTER auto_reparse_attempts,
    ADD KEY idx_ragflow_documents_auto_reparse (
        parse_status, auto_reparse_next_at, auto_reparse_attempts, updated_at
    );
