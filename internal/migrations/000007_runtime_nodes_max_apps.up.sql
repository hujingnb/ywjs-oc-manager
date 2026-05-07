-- runtime_nodes.max_apps 表达节点的最大未删除应用数；NULL 表示不限。
-- OnboardingService 在 onboard 自动选节点时按「剩余容量 = max_apps - 当前应用数」过滤；
-- 平台管理员可通过 PATCH /api/v1/runtime-nodes/:id 设置或清空此字段，运维语义：
--   设为 0 = 暂停接收新应用；设为正数 = 显式上限；置 NULL = 不限。
ALTER TABLE runtime_nodes
    ADD COLUMN max_apps INTEGER;

COMMENT ON COLUMN runtime_nodes.max_apps IS
    '节点最大未删除应用数；NULL 表示不限。OnboardingService 在自动选节点时按剩余容量过滤。';
