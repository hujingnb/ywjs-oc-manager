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
SET status = ?, last_error = ?, updated_at = now()
WHERE app_id = ? AND channel_type = ? AND status <> 'deleted';

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
