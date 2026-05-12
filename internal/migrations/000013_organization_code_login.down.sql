-- 如果迁移后已经创建了跨组织重复 username，这一步会失败，避免静默破坏数据。
ALTER TABLE users
    ADD CONSTRAINT users_username_key UNIQUE (username);

DROP INDEX IF EXISTS users_platform_username_uniq;
DROP INDEX IF EXISTS users_org_username_uniq;

ALTER TABLE organizations DROP CONSTRAINT IF EXISTS organizations_code_key;
ALTER TABLE organizations DROP CONSTRAINT IF EXISTS organizations_code_format_check;
ALTER TABLE organizations DROP COLUMN IF EXISTS code;
