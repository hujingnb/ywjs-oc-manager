-- apps 新增 locale：hermes bot 对终端用户说话的语言。
-- NULL 表示「未显式设置」，由应用层回退到平台默认语言；创建实例时快照 owner 的 locale。
-- 不设 DEFAULT，避免把「未设置」与「显式选了某语言」混淆；CHECK 约束限定取值集合，新增语言时一并扩展。
ALTER TABLE apps
    ADD COLUMN locale VARCHAR(10) NULL COMMENT '应用语言（hermes 对终端用户说话的语言, en/zh）；NULL=用平台默认',
    ADD CONSTRAINT apps_locale_check CHECK (locale IS NULL OR locale IN ('en','zh'));
