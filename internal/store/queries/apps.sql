-- name: CreateApp :exec
-- k8s 模型下 app 对应 Deployment，pod 落点由调度器决定，不再写 runtime_node_id。
-- locale 在创建时快照 owner 的用户语言偏好（NULL=平台回退默认）。
-- knowledge_quota_bytes 由 service 传入所属企业的默认配额，替代 DB 默认 1GB。
INSERT INTO apps (
    id,
    org_id,
    owner_user_id,
    name,
    description,
    status,
    api_key_status,
    version_id,
    locale,
    knowledge_quota_bytes,
    aicc_hidden
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
);

-- name: GetApp :one
SELECT *
FROM apps
WHERE id = ?;

-- name: GetActiveAppByOwner :one
SELECT *
FROM apps
WHERE owner_user_id = ? AND deleted_at IS NULL AND aicc_hidden = FALSE;

-- name: ListAppsByOrg :many
SELECT *
FROM apps
WHERE org_id = ? AND deleted_at IS NULL AND aicc_hidden = FALSE
ORDER BY created_at DESC, id DESC
LIMIT ? OFFSET ?;

-- name: MarkAppAICCHidden :exec
-- AICC 隐藏 app 不出现在普通实例列表中；创建时已写入 true，此查询用于幂等补标记。
UPDATE apps
SET aicc_hidden = TRUE,
    updated_at = now()
WHERE id = ? AND deleted_at IS NULL;

-- name: ListStaleAICCRuntimeApps :many
-- 逐个找出已应用镜像与当前客服专用镜像不一致的隐藏 app。
-- 初始化阶段中的 app 由既有 worker 接管，不能重复入队；每轮 limit=1，避免客服镜像升级时
-- 同时重建全部接待运行时。applied_image_ref 为 NULL 或空值表示历史客服尚未记录专用镜像，也需要升级。
SELECT id
FROM apps
WHERE aicc_hidden = TRUE
  AND deleted_at IS NULL
  AND (applied_image_ref IS NULL OR applied_image_ref <> sqlc.arg(target_image_ref))
  AND status NOT IN ('pulling_runtime_image', 'preparing_runtime', 'creating_container', 'starting')
ORDER BY updated_at ASC, id ASC
LIMIT ?;

-- name: SetAppStatus :exec
UPDATE apps
SET status = ?, updated_at = now()
WHERE id = ?;

-- name: TouchApp :exec
-- 仅刷新 updated_at：worker 等待 pod Ready 期间的心跳。让 reaper 凭 updated_at 区分
-- 「worker 仍在等待（拉镜像可能数十分钟）」与「worker 已死的孤儿」，避免误回收正在处理的 job。
-- 不改 status 或其它字段。
UPDATE apps
SET updated_at = now()
WHERE id = ?;

-- name: SetAppNewAPIKey :exec
UPDATE apps
SET
    newapi_key_id = ?,
    newapi_key_ciphertext = ?,
    api_key_status = ?,
    newapi_key_name = ?,
    updated_at = now()
WHERE id = ?;

-- name: SetAppRuntimeToken :exec
-- 首次写入 per-app control token（三用：bootstrap / oc-kb / oc-ops）；并发重复初始化拿不到行，由 service 读取既有 token。
UPDATE apps
SET runtime_token_hash = ?,
    runtime_token_ciphertext = ?,
    updated_at = now()
WHERE id = ?
  AND deleted_at IS NULL
  AND runtime_token_hash IS NULL
  AND runtime_token_ciphertext IS NULL;

-- name: GetAppByRuntimeTokenHash :one
-- 按 control token（per-app 三用：bootstrap / oc-kb / oc-ops）的 hash 反查当前 app；
-- 不允许请求方传入目标 app/dataset，鉴权即定位。
SELECT *
FROM apps
WHERE runtime_token_hash = ? AND deleted_at IS NULL;

-- name: SoftDeleteApp :exec
UPDATE apps
SET status = 'deleted', deleted_at = now(), updated_at = now()
WHERE id = ? AND deleted_at IS NULL;

-- name: ListRunningApps :many
-- 列出当前期望运行（k8s Deployment 已创建）的应用，供 app_status_reconciler 周期 poll pod 状态。
-- running 是常态；binding_waiting 表示 pod 已起但渠道还在登录中，也需要 reconcile；
-- binding_failed 表示上轮扫码超时，pod 仍在（属渠道发起 allowlist，需 reconciler 维护其 runtime_phase）。
-- spec-A2b：去掉 runtime_node_id / container_id（k8s 路径不再写这两列），消费方仅用 id。
SELECT id
FROM apps
WHERE deleted_at IS NULL
  AND status IN ('running', 'binding_waiting', 'binding_failed')
ORDER BY id;

-- name: SetAppRuntimeSnapshot :exec
UPDATE apps
SET runtime_snapshot_json = ?,
    runtime_snapshot_at = now(),
    updated_at = now()
WHERE id = ?;

-- name: SetAppRuntimePhase :exec
-- 裸 UPDATE runtime_phase(运行时就绪维度,与 status 正交,无状态机守卫,守卫不适用):
-- reconciler 周期写、init worker 首启/就绪写、渠道解绑/升级重启前置 restarting 用。
UPDATE apps
SET runtime_phase = ?, updated_at = now()
WHERE id = ?;

-- name: SetAppRestartPolicy :exec
-- 管理员 PATCH /apps/:appId/restart-policy 写入；mode/max_per_window/window_seconds 校验在 service 层。
UPDATE apps
SET restart_policy_json = ?,
    updated_at = now()
WHERE id = ?;

-- name: SetAppHealthState :exec
-- worker app_health_check handler 写最近一次健康检查结果；用于自动重启窗口计数。
UPDATE apps
SET health_state_json = ?,
    updated_at = now()
WHERE id = ?;

-- name: SetAppProgress :exec
-- progressReporter 节流后写入；NULL/NULL 表示阶段切换或未知。
UPDATE apps
SET progress_current = ?,
    progress_total = ?,
    updated_at = now()
WHERE id = ?;

-- name: ClearAppProgress :exec
-- transitionTo / RequestInitialize 强制清空进度字段。
UPDATE apps
SET progress_current = NULL,
    progress_total = NULL,
    updated_at = now()
WHERE id = ?;

-- name: MarkAppFailed :exec
-- 任意状态 → error 时同时写入来源状态与错误文本，保留"在哪一步失败"与"为什么失败"语义。
-- last_error_status 不加 CHECK 约束，值由调用方在 Go 层负责合法性。
UPDATE apps
SET status = 'error',
    last_error_status = ?,
    last_error_message = ?,
    progress_current = NULL,
    progress_total = NULL,
    updated_at = now()
WHERE id = ?;

-- name: ListStaleInits :many
-- reaper 扫描 init 子状态下「连续 N 秒无更新」的孤儿；N 由调用方按秒传入。
-- 包含新旧两套 init 状态，确保历史孤儿也能被正确清理。
-- spec-A2b：去掉 runtime_node_id（k8s 路径不再写该列），reaper 仅需 id / status 重置孤儿。
-- 关键：阈值用 SQL 侧 now() - INTERVAL 计算，而非接收 Go 侧 time.Now() 阈值。
-- updated_at 由 now() 写入，二者同处服务器时钟与会话时区，根除「Go 时间 vs now() 列」
-- 跨时区比较错位（曾因 DSN loc=UTC 与服务器 +08:00 不匹配导致本查询恒返回 0，孤儿永不回收）。
SELECT id, status
FROM apps
WHERE deleted_at IS NULL
  AND status IN ('pulling_runtime_image','preparing_runtime','creating_container','starting')
  AND updated_at < now() - INTERVAL ? SECOND
ORDER BY id;

-- name: ListErrorApps :many
-- reconciler 兜底用：列出 status=error 的 app。reconciler 查其 pod，若 hermes 实际 Ready
-- （说明并非真失败，只是状态没收敛，如 WaitReady 曾误超时但 pod 后来起来了），就重新入队
-- init job 推进到 running；pod 真坏则保持 error 不动。reaper 只扫 init 子状态、不管 error，
-- 此查询补上「init 失败成 error 后无法自愈」的洞。
SELECT id
FROM apps
WHERE deleted_at IS NULL
  AND status = 'error'
ORDER BY id;

-- name: ListRestartingApps :many
-- reconciler 收敛用：列出 status=restarting 的 app id。渠道解绑触发 RolloutRestart 后
-- 实例置 restarting，pod 重建（Recreate）期间 oc-ops 不可用；reconciler 周期查其 pod 状态，
-- pod 重新 Ready → 收敛回 running，pod 坏死 → error，重启空窗（Pending）→ 保持 restarting 等下轮。
SELECT id
FROM apps
WHERE deleted_at IS NULL
  AND status = 'restarting'
ORDER BY id;

-- name: UpdateAppRuntimeImage :exec
-- phasePullRuntimeImage 成功后写入镜像引用与 sha256。
UPDATE apps
SET
    runtime_image_ref    = ?,
    runtime_image_sha256 = ?,
    updated_at = now()
WHERE id = ?;

-- name: SetAppAppliedVersion :exec
-- 初始化/重启成功后记录已应用的版本修订与镜像 ref，用于 version_synced 检测。
UPDATE apps
SET applied_version_revision = ?,
    applied_image_ref = ?,
    updated_at = now()
WHERE id = ?;

-- name: SetAppWebPublishApplied :exec
-- bootstrap 渲染时记录本次是否注入了 web-publish 发布能力，用于「能力已开通需重启」检测。
-- 不更新 updated_at：bootstrap 每次 pod 启动都会调用，避免无意义地刷新 updated_at。
UPDATE apps
SET web_publish_applied = ?
WHERE id = ?;

-- name: SetAppAppliedPlatformPromptHash :exec
-- bootstrap 渲染时记录本次写入 input 的平台层 prompt sha256，用于「平台提示词已更新需重启」检测。
-- 不更新 updated_at：bootstrap 每次 pod 启动都会调用（与 SetAppWebPublishApplied 同因），
-- 避免无意义地刷新 updated_at。
UPDATE apps
SET applied_platform_prompt_hash = ?
WHERE id = ?;

-- name: SetAppVersion :exec
-- 切换实例绑定的助手版本，并把 applied_version_revision / applied_image_ref 清零。
-- 不同版本各自维护独立的 revision 计数，若切换后保留旧 applied_*，当新旧版本
-- 的 revision 数字恰好相同（且镜像相同）时 version_synced 会误判为已同步。
-- 清零后 applied_version_revision=0 永远不等于任何真实版本 revision（从 1 起），
-- 实例切换后必然进入需重启态，直到重启重新写入 applied_*。
UPDATE apps
SET version_id = ?,
    applied_version_revision = 0,
    applied_image_ref = '',
    updated_at = now()
WHERE id = ? AND deleted_at IS NULL;

-- name: GetAppWithVersion :one
-- 取实例及其绑定版本的 revision / image_id，供 version_synced 计算。
SELECT sqlc.embed(apps), av.revision AS version_revision, av.image_id AS version_image_id
FROM apps
JOIN assistant_versions av ON av.id = apps.version_id
WHERE apps.id = ?;

-- name: ListAppsByOrgWithVersion :many
-- 组织实例列表联查版本 revision / image_id，供 version_synced 批量计算。
SELECT sqlc.embed(apps), av.revision AS version_revision, av.image_id AS version_image_id
FROM apps
JOIN assistant_versions av ON av.id = apps.version_id
WHERE apps.org_id = ? AND apps.deleted_at IS NULL AND apps.aicc_hidden = FALSE
ORDER BY apps.created_at DESC, apps.id DESC
LIMIT ? OFFSET ?;

-- name: CountActiveAppsByOrg :one
-- 统计企业当前未删除普通实例数；AICC 隐藏 app 使用独立 aicc_agent_limit，不占用普通实例上限。
SELECT COUNT(*) FROM apps WHERE org_id = ? AND deleted_at IS NULL AND aicc_hidden = FALSE;

-- name: UpdateAppLocale :exec
-- 更新实例语言偏好（hermes 对终端用户说话的语言）。locale 由 service 层校验合法取值后传入。
UPDATE apps
SET locale = ?,
    updated_at = now()
WHERE id = ? AND deleted_at IS NULL;
