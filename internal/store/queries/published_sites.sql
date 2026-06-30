-- name: GetPublishedSiteByHost :one
SELECT * FROM published_sites WHERE host = ?;

-- name: GetPublishedSiteByID :one
SELECT * FROM published_sites WHERE id = ?;

-- name: CountActiveSitesByOrg :one
SELECT COUNT(*) FROM published_sites WHERE org_id = ? AND status = 'active';

-- name: ListActiveSites :many
SELECT host, id, s3_prefix, status FROM published_sites WHERE status = 'active';

-- name: ListSitesByOrg :many
-- 联查发布者（站点归属实例 app 的 owner 用户），供管理面展示「哪个用户发布的」。
-- LEFT JOIN：实例/用户即便被软删也不丢站点行，发布者信息回退为 NULL。
SELECT sqlc.embed(ps), u.display_name AS owner_display_name, u.username AS owner_username
FROM published_sites ps
LEFT JOIN apps a ON a.id = ps.app_id
LEFT JOIN users u ON u.id = a.owner_user_id
WHERE ps.org_id = ?
ORDER BY ps.updated_at DESC;

-- name: CreatePublishedSite :exec
INSERT INTO published_sites (
    id, org_id, app_id, host, slug, current_version, s3_prefix, status, size_bytes, expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, 'active', ?, ?);

-- name: SetPublishedSiteStatus :exec
UPDATE published_sites SET status = ?, updated_at = now() WHERE id = ?;

-- name: ListExpiredActiveSites :many
SELECT * FROM published_sites WHERE status = 'active' AND expires_at < now();

-- name: RenewPublishedSite :exec
-- 续期：把过期时间延后到 now + N 天（N 由 service 按企业 site_ttl_days 传入），并置回 active。
UPDATE published_sites
SET expires_at = ?, status = 'active', updated_at = now()
WHERE id = ?;
