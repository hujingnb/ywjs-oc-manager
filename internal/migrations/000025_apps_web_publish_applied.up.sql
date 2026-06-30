-- apps 增加 web_publish_applied：记录实例「最近一次 bootstrap 渲染时是否注入了 web-publish 发布能力」。
-- 用途：企业 web-publish 能力是在实例 bootstrap 时按「企业已开通且 provisioning ready」条件注入的；
-- 若企业在实例已运行之后才开通，运行中的实例并不具备发布能力，需重启重新 bootstrap 才能获得。
-- 该标记与 org_web_publish_config 的开通态比对即可判定实例是否「需重启使发布能力生效」
-- （与 applied_version_revision 驱动 version_synced 的思路一致）。
-- 默认 0：存量实例与新建实例在首次（重新）bootstrap 前均视为未注入。
ALTER TABLE apps
    ADD COLUMN web_publish_applied TINYINT(1) NOT NULL DEFAULT 0
        COMMENT '最近一次 bootstrap 是否注入 web-publish 发布能力：1=已注入，0=未注入（与企业开通态比对判定是否需重启）';
