-- apps_org_owner_status_idx 重建为匹配 ListAppsByOrg 查询的索引顺序。
-- 旧索引 (org_id, owner_user_id, status) 不利于 WHERE org_id=? AND deleted_at IS NULL
-- ORDER BY created_at DESC 这种最常见的列表查询。
DROP INDEX IF EXISTS apps_org_owner_status_idx;
CREATE INDEX apps_org_active_created_idx ON apps(org_id, deleted_at, created_at DESC);
