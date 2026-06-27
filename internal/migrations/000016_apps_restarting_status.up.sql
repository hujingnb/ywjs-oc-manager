-- 实例 restarting 状态：渠道解绑触发 RolloutRestart 重建 pod 期间的过渡态，
-- 由 reconciler 在 pod 重新 Ready 后收敛回 running。放宽 apps_status_check，
-- 在现有取值集合里追加 'restarting'（其余取值保持与 000001 基线一致）。
ALTER TABLE apps
    DROP CONSTRAINT apps_status_check,
    ADD CONSTRAINT apps_status_check CHECK (status IN (
        'draft','pulling_runtime_image','pulling_image','syncing_image','preparing_runtime',
        'creating_container','starting','binding_waiting','binding_failed',
        'running','stopped','error','deleted','restarting'));
