-- name: GetWebPublishConfig :one
-- 按企业取发布能力配置；不存在返回 sql.ErrNoRows（视为未开通）。
SELECT * FROM org_web_publish_config WHERE org_id = ?;

-- name: ListWebPublishConfigs :many
-- 平台管理员全局视图：列出所有企业的发布能力配置。
SELECT * FROM org_web_publish_config ORDER BY updated_at DESC;

-- name: UpsertWebPublishConfig :exec
-- 平台管理员配置/改配置：写基础域名 / provider / 凭证密文 / 配额。
-- 不触碰 provisioning_status 与 cert_* 状态（那由状态机维护），首插时取列默认值。
INSERT INTO org_web_publish_config (
    org_id, base_domain, dns_provider, dns_credentials_ciphertext, site_ttl_days, max_sites
) VALUES (?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
    base_domain                = VALUES(base_domain),
    dns_provider               = VALUES(dns_provider),
    dns_credentials_ciphertext = VALUES(dns_credentials_ciphertext),
    site_ttl_days              = VALUES(site_ttl_days),
    max_sites                  = VALUES(max_sites),
    updated_at                 = now();

-- name: SetWebPublishEnabled :exec
-- 开通/停用：置 enabled 与 provisioning_status（开通时由 service 传 'provisioning'）。
UPDATE org_web_publish_config
SET enabled = ?, provisioning_status = ?, updated_at = now()
WHERE org_id = ?;

-- name: SetWebPublishProvisioning :exec
-- 状态机更新 provisioning 结果：状态 + 摘要 + 证书 Secret 名。
UPDATE org_web_publish_config
SET provisioning_status = ?, provisioning_message = ?, cert_secret_name = ?, updated_at = now()
WHERE org_id = ?;

-- name: SetWebPublishCertStatus :exec
-- 状态机/巡检更新证书状态：状态 + 到期 + 最近签发时间 + 最近续签时间 + 摘要。
-- cert_last_renewed_at 用 COALESCE 跳过传 NULL 的场景（首签只更新 issued_at，续签更新 renewed_at）。
UPDATE org_web_publish_config
SET cert_status = ?, cert_not_after = ?, cert_last_issued_at = ?, cert_last_renewed_at = COALESCE(?, cert_last_renewed_at), cert_message = ?, updated_at = now()
WHERE org_id = ?;

-- name: ListConfigsCertExpiringBefore :many
-- 证书续签巡检：列出已签发且 cert_not_after 早于阈值的企业配置（需续签）。
SELECT * FROM org_web_publish_config
WHERE enabled = 1
  AND provisioning_status = 'ready'
  AND cert_status = 'issued'
  AND cert_not_after IS NOT NULL
  AND cert_not_after < ?;
