-- 回滚：仅 DROP 本列与约束；apps_status_check 从未改动，回滚后旧代码完全不受影响。
ALTER TABLE apps
    DROP CONSTRAINT apps_runtime_phase_check,
    DROP COLUMN runtime_phase;
