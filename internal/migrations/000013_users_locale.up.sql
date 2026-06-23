-- users 新增 locale：用户界面语言偏好。NULL 表示「未显式选择」，由应用层回退到平台默认语言。
-- 不设 DEFAULT，避免把「未选择」与「显式选了某语言」混淆；CHECK 约束限定取值集合，新增语言时一并扩展。
ALTER TABLE users
    ADD COLUMN locale VARCHAR(10) NULL COMMENT '用户界面语言偏好（en/zh）；NULL=未选择，回退平台默认',
    ADD CONSTRAINT users_locale_check CHECK (locale IS NULL OR locale IN ('en','zh'));
