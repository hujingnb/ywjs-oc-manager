-- name: CreateChannelBinding :exec
INSERT INTO channel_bindings (
    id,
    app_id,
    channel_type,
    status
) VALUES (
    ?, ?, ?, ?
);

-- name: GetChannelBindingByAppAndType :one
SELECT *
FROM channel_bindings
WHERE app_id = ? AND channel_type = ? AND status <> 'deleted';

-- name: SetChannelBindingStatus :exec
UPDATE channel_bindings
SET
    status = sqlc.arg(status),
    last_error = sqlc.arg(last_error),
    -- SetChannelBindingStatus 只承载 pending / failed / expired / unbound 等非 bound 状态；
    -- bound 统一走 MarkChannelBindingBound。因此这里清理旧身份，避免解绑或失败后前端展示 stale account。
    -- metadata_json 不能在 pending_auth 轮询中清理：channel_start_login 写入二维码后，
    -- channel_check_binding 会继续把状态刷新为 pending_auth，若清 metadata 会导致二维码立即消失。
    bound_identity = NULL,
    channel_name = NULL,
    bound_at = NULL,
    updated_at = now()
WHERE app_id = sqlc.arg(app_id) AND channel_type = sqlc.arg(channel_type) AND status <> 'deleted';

-- name: SetChannelBindingChallenge :exec
UPDATE channel_bindings
SET status = 'pending_auth', metadata_json = ?, last_error = NULL, updated_at = now()
WHERE app_id = ? AND channel_type = ? AND status <> 'deleted';

-- name: MarkChannelBindingBound :exec
UPDATE channel_bindings
SET
    status = 'bound',
    bound_identity = ?,
    channel_name = ?,
    metadata_json = ?,
    bound_at = now(),
    last_error = NULL,
    updated_at = now()
WHERE app_id = ? AND channel_type = ? AND status <> 'deleted';

-- name: CountChannelBindingsByApp :one
-- 统计指定应用下未被标记为 deleted 的渠道绑定数。
-- RuntimeOperationService.Trigger 在写 delete 审计前调用，把数量塞进 detail_message。
SELECT COUNT(*) AS count
FROM channel_bindings
WHERE app_id = ? AND status <> 'deleted';

-- name: AppHasBoundChannelBinding :one
-- 判断指定应用下是否存在 status='bound' 的渠道绑定。
-- app_initialize 在推进到 binding_waiting 之后调用：若发现已 bound（如切换助手
-- 版本触发镜像重建后、容器重启前渠道凭证依旧落在 bind mount 目录、无需用户
-- 重新扫码），则直接把 status 推到 running，避免概览页长期卡在「待绑定」。
SELECT EXISTS (
    SELECT 1
    FROM channel_bindings
    WHERE app_id = ? AND status = 'bound'
) AS has_bound;

-- name: UpsertChannelBindingUnbound :exec
-- 飞书无预建绑定行，BeginAuth 时 create-on-demand（已存在则忽略）。
-- app_active_key 是 VIRTUAL 生成列（非 deleted 行 = app_id），不能显式赋值，
-- ON DUPLICATE KEY 命中唯一约束 (app_active_key, channel_type) 时做 no-op。
INSERT INTO channel_bindings (id, app_id, channel_type, status)
VALUES (?, ?, ?, 'unbound')
ON DUPLICATE KEY UPDATE id = id;

-- name: SetFeishuCredentials :exec
-- 写入飞书凭证 metadata（app_id 明文 + secret 密文 + domain + bot 信息 + injected 标记）并置状态。
-- 供 BeginAuth service（Task 14）与凭证注入 worker（Task 17）调用。
UPDATE channel_bindings
SET metadata_json = ?, status = ?, last_error = NULL, updated_at = now()
WHERE app_id = ? AND channel_type = 'feishu' AND status <> 'deleted';
