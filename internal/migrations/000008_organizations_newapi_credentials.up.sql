-- 组织持有 new-api 业务用户凭据，避免应用初始化时反复登录拿 access_token。
-- 明文为 JSON {username, password, access_token}，使用 manager 的 security.master_key
-- AES-256-GCM 加密；密文放 organizations 表的列上是因为 user 与 org 一一对应，
-- 不为这份凭据单建表降低运维复杂度。

ALTER TABLE organizations
    ADD COLUMN newapi_user_credentials_ciphertext text NULL;

COMMENT ON COLUMN organizations.newapi_user_credentials_ciphertext IS
    'new-api 业务用户凭据密文，明文为 JSON {username, password, access_token}，使用 manager 的 security.master_key AES-256-GCM 加密。';
