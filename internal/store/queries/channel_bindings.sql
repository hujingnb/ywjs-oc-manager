-- name: CreateChannelBinding :one
INSERT INTO channel_bindings (
    app_id,
    channel_type,
    status
) VALUES (
    $1, $2, $3
)
RETURNING *;

-- name: GetChannelBindingByAppAndType :one
SELECT *
FROM channel_bindings
WHERE app_id = $1 AND channel_type = $2 AND status <> 'deleted';

-- name: SetChannelBindingStatus :one
UPDATE channel_bindings
SET status = $3, last_error = $4, updated_at = now()
WHERE app_id = $1 AND channel_type = $2 AND status <> 'deleted'
RETURNING *;

-- name: SetChannelBindingChallenge :one
UPDATE channel_bindings
SET status = 'pending_auth', metadata_json = $3, last_error = NULL, updated_at = now()
WHERE app_id = $1 AND channel_type = $2 AND status <> 'deleted'
RETURNING *;

-- name: MarkChannelBindingBound :one
UPDATE channel_bindings
SET
    status = 'bound',
    bound_identity = $3,
    channel_name = $4,
    metadata_json = $5,
    bound_at = now(),
    last_error = NULL,
    updated_at = now()
WHERE app_id = $1 AND channel_type = $2 AND status <> 'deleted'
RETURNING *;

-- name: CountChannelBindingsByApp :one
-- 统计指定应用下未被标记为 deleted 的渠道绑定数。
-- RuntimeOperationService.Trigger 在写 delete 审计前调用，把数量塞进 detail_message。
SELECT COUNT(*)::bigint AS count
FROM channel_bindings
WHERE app_id = $1 AND status <> 'deleted';
