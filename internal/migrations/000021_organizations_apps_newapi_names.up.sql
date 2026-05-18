-- organizations.newapi_username 存组织在 new-api 一侧的 user 名。
-- 当前实现里它与 organizations.code 同值，但语义不同：
-- code 是 manager 内部组织标识，username 是远端 new-api 的 user.username。
-- 拆开存避免未来 new-api 命名规则变化时隐式破坏过滤。
ALTER TABLE organizations ADD COLUMN newapi_username text NULL;
COMMENT ON COLUMN organizations.newapi_username IS 'new-api 侧的 user.username，用于按 username 过滤用量响应';

-- 回填：已创建 new-api user 的组织，username 等于 code（实测一致）。
UPDATE organizations
SET newapi_username = code
WHERE newapi_user_id IS NOT NULL AND newapi_user_id <> '';

-- apps.newapi_key_name 存实例在 new-api 一侧的 token name。
-- 当前实现 = "app-" + app.id，但同样分开存。
ALTER TABLE apps ADD COLUMN newapi_key_name text NULL;
COMMENT ON COLUMN apps.newapi_key_name IS 'new-api 侧的 token.name，用于按 token_name 过滤用量日志';

-- 回填：已绑定 new-api token 的实例。
UPDATE apps
SET newapi_key_name = 'app-' || id::text
WHERE newapi_key_id IS NOT NULL AND newapi_key_id <> '';
