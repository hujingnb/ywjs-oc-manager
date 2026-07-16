-- 记录由已确认咨询重新开始时的首条访客消息，使状态卡片可以按阶段稳定恢复。
ALTER TABLE aicc_sessions
    ADD COLUMN resolution_phase_start_message_id VARCHAR(36) NULL AFTER resolution_status;
