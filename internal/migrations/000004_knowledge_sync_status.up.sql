-- knowledge_sync_status 表记录组织级知识库每个 (org_id, node_id) 对的最近同步状态。
--
-- 选型理由（spec §5.3 复用判断）：
--   1. audit_logs 是事件流不能稳定表达"该 (org, node) 当前态"——例如最近一次
--      sync 是 7 天前 success，其后没新事件，此时是同步还是过期？
--   2. 重试入口需要写"待同步"中间态，audit_logs 是 append-only 写不进。
--   3. 单次 /sync-status 查询要 N 个 (org, node) 对，audit_logs 聚合开销 P95 容易超 100ms。
--
-- 字段：
--   - status 取 'pending' | 'synced' | 'failed'
--   - last_success_at: 最近一次推到 'synced' 的时间
--   - last_error: failed 时的错误片段（截断 500 字符）
--   - updated_at: 任何状态翻转的时间
--
-- 主键 (org_id, node_id) 让 upsert 直接 ON CONFLICT。

CREATE TABLE knowledge_sync_status (
    org_id          uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    node_id         uuid NOT NULL REFERENCES runtime_nodes(id) ON DELETE CASCADE,
    status          text NOT NULL CHECK (status IN ('pending', 'synced', 'failed')),
    last_success_at timestamptz NULL,
    last_error      text NULL,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, node_id)
);

-- 按 org 列出所有节点状态的常见查询；status / updated_at 索引按需添加。
CREATE INDEX idx_knowledge_sync_status_org ON knowledge_sync_status(org_id);
