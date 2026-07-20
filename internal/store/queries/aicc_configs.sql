-- name: CreateOrganizationAICCConfig :exec
-- 新企业默认关闭，但需从有效首版本初始化模型，保证旧启用接口保留模型时不会触发 enabled/model CHECK。
INSERT INTO organization_aicc_configs (org_id, model)
SELECT o.id, av.main_model
FROM organizations o
LEFT JOIN assistant_versions av
  ON av.id = JSON_UNQUOTE(JSON_EXTRACT(o.assistant_version_ids, '$[0]'))
 AND av.deleted_at IS NULL
WHERE o.id = ?;

-- name: GetOrganizationAICCConfig :one
-- 按企业读取独立 AICC 配置，调用方据此校验开通状态和当前模型 revision。
SELECT * FROM organization_aicc_configs WHERE org_id = ?;

-- name: GetOrganizationAICCConfigForUpdate :one
-- 配置更新事务先锁定企业配置行，串行化并发模型变更，避免 revision 丢失更新。
SELECT * FROM organization_aicc_configs WHERE org_id = ? FOR UPDATE;

-- name: ListOrganizationAICCConfigs :many
-- 平台管理场景按稳定企业主键顺序返回配置，避免分页或批处理顺序漂移。
SELECT * FROM organization_aicc_configs ORDER BY org_id;

-- name: UpdateOrganizationAICCConfig :exec
-- revision 由业务层在配置实际变化时递增，更新时间显式落库便于追踪 rollout 起点。
UPDATE organization_aicc_configs
SET enabled = ?,
    model = ?,
    agent_limit = ?,
    revision = ?,
    updated_at = NOW()
WHERE org_id = ?;

-- name: ListPendingAICCModelRolloutAgents :many
-- 仅选择仍有效且活跃的智能体；app 软删除后不得继续下发企业模型配置。
SELECT aa.*
FROM aicc_agents aa
JOIN apps a ON a.id = aa.app_id AND a.deleted_at IS NULL
WHERE aa.org_id = ?
  AND aa.deleted_at IS NULL
  AND aa.status = 'active'
  AND aa.applied_config_revision < ?
ORDER BY aa.id
LIMIT ?;

-- name: SetAICCAgentAppliedConfigRevision :exec
-- 条件更新只允许 revision 前进，防止较旧的并发 rollout 覆盖新配置应用进度。
UPDATE aicc_agents
SET applied_config_revision = ?, updated_at = NOW()
WHERE id = ? AND applied_config_revision < ?;
