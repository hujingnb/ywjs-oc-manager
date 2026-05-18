-- audit_logs 加 detail_message 列，存写入端冻结的详情字符串。
-- NULL 表示无详情或老数据；前端展示「—」。
ALTER TABLE audit_logs ADD COLUMN detail_message text NULL;
COMMENT ON COLUMN audit_logs.detail_message IS '事件详情快照，由写入端拼装。NULL 表示无详情或老数据。';
