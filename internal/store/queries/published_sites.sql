-- name: GetPublishedSiteByHost :one
SELECT * FROM published_sites WHERE host = ?;

-- name: GetPublishedSiteByID :one
SELECT * FROM published_sites WHERE id = ?;

-- name: CountActiveSitesByOrg :one
SELECT COUNT(*) FROM published_sites WHERE org_id = ? AND status = 'active';

-- name: ListActiveSites :many
SELECT host, id, s3_prefix, status FROM published_sites WHERE status = 'active';

-- name: ListSitesByOrg :many
SELECT * FROM published_sites WHERE org_id = ? ORDER BY updated_at DESC;

-- name: CreatePublishedSite :exec
INSERT INTO published_sites (
    id, org_id, app_id, host, slug, current_version, s3_prefix, status, size_bytes, expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, 'active', ?, ?);

-- name: UpdatePublishedSiteVersion :exec
UPDATE published_sites
SET current_version = ?, s3_prefix = ?, size_bytes = ?, status = 'active', expires_at = ?, updated_at = now()
WHERE id = ?;

-- name: SetPublishedSiteStatus :exec
UPDATE published_sites SET status = ?, updated_at = now() WHERE id = ?;

-- name: ListExpiredActiveSites :many
SELECT * FROM published_sites WHERE status = 'active' AND expires_at < now();

-- name: RenewPublishedSite :exec
-- 续期：把过期时间延后到 now + N 天（N 由 service 按企业 site_ttl_days 传入），并置回 active。
UPDATE published_sites
SET expires_at = ?, status = 'active', updated_at = now()
WHERE id = ?;
