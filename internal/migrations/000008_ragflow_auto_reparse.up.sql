-- RAGFlow 自动重解析重试状态：
-- auto_reparse_attempts 记录已成功提交给 RAGFlow 的自动重试次数（不含失败提交），用于封顶最多 3 次自动重试。
-- auto_reparse_next_at 为下一次允许自动重试的时间：NULL 表示当前不参与自动重试；非 NULL 且已到时表示到期可重试。
-- 复合索引覆盖后台扫描「到期可自动重解析的 failed 文档」的查询条件与排序。
ALTER TABLE ragflow_documents
    ADD COLUMN auto_reparse_attempts INT NOT NULL DEFAULT 0 AFTER last_error,
    ADD COLUMN auto_reparse_next_at DATETIME(6) NULL AFTER auto_reparse_attempts,
    ADD KEY idx_ragflow_documents_auto_reparse (
        parse_status,
        auto_reparse_next_at,
        auto_reparse_attempts,
        updated_at
    );

-- 存量回填：把历史上因模型服务过载（临时上游故障）而失败的文档标记为立即可自动重试。
-- 仅迁移设置可重试时间，真正的重新提交由后台刷新任务在迁移之后执行，迁移本身不调用 RAGFlow。
-- 必须用 UTC_TIMESTAMP(6) 而非 NOW(6)：auto_reparse_next_at 按 UTC 存取（app DB 连接固定
-- time_zone='+00:00'/loc=UTC，其扫描查询 NOW(6) 为 UTC），但迁移连接走服务器 SYSTEM 时区
-- （移动云托管 MySQL 常为 +08:00），NOW(6) 会写入北京墙钟裸值、比 app 的 UTC 基准早 8 小时，
-- 使回填文档要再等 8 小时才被判为到期；UTC_TIMESTAMP(6) 与会话时区无关、恒为 UTC，二者一致。
UPDATE ragflow_documents
SET auto_reparse_next_at = UTC_TIMESTAMP(6)
WHERE parse_status = 'failed'
  AND auto_reparse_attempts = 0
  AND last_error IS NOT NULL
  AND (
      LOWER(last_error) LIKE '%model service overloaded%'
      OR LOWER(last_error) LIKE '%error code: 503%'
      OR LOWER(last_error) LIKE '%code: 50505%'
  );
