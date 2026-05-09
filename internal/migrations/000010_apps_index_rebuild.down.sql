-- 回滚到原索引（org_id, owner_user_id, status）。
DROP INDEX IF EXISTS apps_org_active_created_idx;
CREATE INDEX apps_org_owner_status_idx ON apps(org_id, owner_user_id, status);
