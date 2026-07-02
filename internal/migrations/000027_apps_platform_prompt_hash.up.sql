-- apps 增加 applied_platform_prompt_hash：记录实例「最近一次 bootstrap 写入 input 的平台层
-- prompt 文本」的 sha256（hex）。平台层 prompt 已固化为代码常量 config.DefaultSystemPromptTemplate，
-- 只在 manager 重新部署时变化；实例只有走一次 bootstrap/重启才会重渲染 SOUL.md 平台层。
-- 该 hash 与当前常量 hash 比对即可判定实例是否「平台提示词已更新、需重启生效」
-- （与 web_publish_applied / applied_version_revision 的快照-比对思路一致）。
-- 默认 ''：存量实例在首次（重新）bootstrap 前 hash 为空，一律判为需重启。
ALTER TABLE apps
    ADD COLUMN applied_platform_prompt_hash CHAR(64) NOT NULL DEFAULT ''
        COMMENT '最近一次 bootstrap 写入 input 的平台层 prompt 文本 sha256（hex）：与当前常量 hash 比对判定是否需重启生效；空=存量/未 bootstrap，视为需重启';
