-- 实例运行时就绪维度 runtime_phase：与业务态 status 正交，描述 pod 此刻能否服务。
-- ready=所有关键容器就绪可服务；starting=首次拉起中未就绪；restarting=重启窗口
-- (解绑/升级/k8s 自发)oc-ops 暂不可用；unknown=未探明。
-- 纯增量：不动 apps_status_check，滚动部署期新旧 manager 代码并存安全；
-- DEFAULT 'unknown' 让旧二进制 INSERT(不带本列)与新建 draft 实例都拿到合理初值。
ALTER TABLE apps
    ADD COLUMN runtime_phase VARCHAR(20) NOT NULL DEFAULT 'unknown'
        COMMENT '运行时就绪维度(与status正交):ready/starting/restarting/unknown',
    ADD CONSTRAINT apps_runtime_phase_check
        CHECK (runtime_phase IN ('ready','starting','restarting','unknown'));

-- 存量行乐观回填：running→ready(避免升级后所有运行实例被闸门拦~15s)，
-- restarting→restarting(存量解绑重启窗)，其余→unknown；reconciler 下一个 tick(~15s)
-- 用真实 pod 探测自愈纠正任何回填偏差。
UPDATE apps SET runtime_phase = CASE status
    WHEN 'running'    THEN 'ready'
    WHEN 'restarting' THEN 'restarting'
    ELSE 'unknown'
END;
