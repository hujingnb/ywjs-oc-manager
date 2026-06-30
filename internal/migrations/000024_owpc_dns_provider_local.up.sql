-- 放宽 org_web_publish_config.dns_provider 的 CHECK 约束，新增 'local'（本地调试占位 provider）。
-- 'local' 仅在平台开启 dev_self_signed_cert 时由 service 层允许选用，配合自签证书在本地走完整开通流程；
-- 000021 建表时约束只含四家真实云 provider，落库 'local' 会触发 Error 3819
-- Check constraint 'owpc_dns_provider_check' is violated。本迁移补齐该取值。
ALTER TABLE org_web_publish_config
    DROP CONSTRAINT owpc_dns_provider_check,
    ADD CONSTRAINT owpc_dns_provider_check CHECK (dns_provider IN ('','alidns','huaweicloud','tencentcloud','cmcccloud','local'));
