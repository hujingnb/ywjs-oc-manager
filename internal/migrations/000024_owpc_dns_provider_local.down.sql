-- 回滚：移除 'local'，还原 000021 的 owpc_dns_provider_check 取值集合。
ALTER TABLE org_web_publish_config
    DROP CONSTRAINT owpc_dns_provider_check,
    ADD CONSTRAINT owpc_dns_provider_check CHECK (dns_provider IN ('','alidns','huaweicloud','tencentcloud','cmcccloud'));
