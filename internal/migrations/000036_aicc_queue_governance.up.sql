-- 单行锁把全局队列容量判断与任务创建串行化，避免多个 manager 副本同时越过上限。
CREATE TABLE aicc_queue_governance (
    id TINYINT NOT NULL PRIMARY KEY,
    CONSTRAINT aicc_queue_governance_singleton CHECK (id = 1)
);
INSERT INTO aicc_queue_governance (id) VALUES (1);
