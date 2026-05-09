-- users 加 deleted_at 字段；语义=「下线时间戳」（status=disabled 时同步设置，
-- enabled 时清空）。注意：与 organizations.deleted_at 真删除时间语义不同；
-- AGENTS.md 已加约定避免运维误解。
ALTER TABLE users ADD COLUMN deleted_at TIMESTAMPTZ NULL;

-- 部分索引：仅活跃用户（deleted_at IS NULL）的查询走该索引
CREATE INDEX users_active_idx ON users(deleted_at) WHERE deleted_at IS NULL;
