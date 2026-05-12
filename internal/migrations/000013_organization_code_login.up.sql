-- organizations.code 是组织登录命名空间，创建后不可通过业务接口修改。
ALTER TABLE organizations ADD COLUMN code text NULL;

-- 历史组织自动生成稳定 code：
-- 1. 英文/数字组织名转小写 slug；
-- 2. 中文或其它无法转出有效 slug 的名称使用 org-<uuid8>；
-- 3. slug 冲突时追加 uuid8，避免唯一约束失败。
WITH raw AS (
    SELECT
        id,
        btrim(lower(regexp_replace(name, '[^a-zA-Z0-9]+', '-', 'g')), '-') AS slug
    FROM organizations
),
base AS (
    SELECT
        id,
        CASE
            WHEN slug ~ '^[a-z0-9]([a-z0-9-]{1,30}[a-z0-9])$'
                THEN slug
            ELSE 'org-' || left(replace(id::text, '-', ''), 8)
        END AS base_code
    FROM raw
),
ranked AS (
    SELECT
        id,
        base_code,
        count(*) OVER (PARTITION BY base_code) AS same_code_count
    FROM base
),
resolved AS (
    SELECT
        id,
        CASE
            WHEN same_code_count = 1 THEN base_code
            ELSE btrim(left(base_code, 23), '-') || '-' || left(replace(id::text, '-', ''), 8)
        END AS code
    FROM ranked
)
UPDATE organizations
SET code = resolved.code
FROM resolved
WHERE organizations.id = resolved.id;

ALTER TABLE organizations ALTER COLUMN code SET NOT NULL;

ALTER TABLE organizations
    ADD CONSTRAINT organizations_code_format_check
    CHECK (code ~ '^[a-z0-9]([a-z0-9-]{1,30}[a-z0-9])$');

ALTER TABLE organizations
    ADD CONSTRAINT organizations_code_key UNIQUE (code);

-- users.username 从全局唯一改为按账号归属范围唯一：
-- 平台管理员无 org_id，平台范围内 username 唯一；
-- 组织用户有 org_id，同组织内 username 唯一，不同组织可重复。
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_username_key;

CREATE UNIQUE INDEX users_org_username_uniq
    ON users(org_id, username)
    WHERE org_id IS NOT NULL;

CREATE UNIQUE INDEX users_platform_username_uniq
    ON users(username)
    WHERE org_id IS NULL;
