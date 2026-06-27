-- 还原 apps_status_check 至不含 'restarting' 的基线取值集合。
-- 注意：若回滚时仍有 app 行处于 status='restarting'，此约束重建会因 CHECK 冲突失败，属预期
-- （回滚前需先把这些行收敛回 running/error 等合法状态）。
ALTER TABLE apps
    DROP CONSTRAINT apps_status_check,
    ADD CONSTRAINT apps_status_check CHECK (status IN (
        'draft','pulling_runtime_image','pulling_image','syncing_image','preparing_runtime',
        'creating_container','starting','binding_waiting','binding_failed',
        'running','stopped','error','deleted'));
