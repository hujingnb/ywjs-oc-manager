-- org_web_publish_config 存每个企业的"网站发布"能力配置与一次性 provisioning / 证书托管状态。
-- 单独建表（不塞进 organizations）以隔离 provider 凭证密文与证书状态字段。
CREATE TABLE org_web_publish_config (
    org_id                     CHAR(36)     NOT NULL COMMENT '所属企业 ID（主键即一企业一行）',
    enabled                    TINYINT(1)   NOT NULL DEFAULT 0 COMMENT '能力总开关：1 开通、0 停用',
    base_domain                VARCHAR(255) NOT NULL DEFAULT '' COMMENT '企业基础域名，站点为 <slug>.<base_domain>',
    dns_provider               VARCHAR(32)  NOT NULL DEFAULT '' COMMENT 'DNS provider：alidns/huaweicloud/tencentcloud/cmcccloud',
    dns_credentials_ciphertext TEXT         NULL COMMENT 'provider 凭证 JSON 的 auth.Cipher 密文（不落明文/不进日志）',
    site_ttl_days              INT          NOT NULL DEFAULT 7  COMMENT '站点默认存活天数（发布/续期用）',
    max_sites                  INT          NOT NULL DEFAULT 20 COMMENT '该企业最多同时存在的已发布站点数',
    provisioning_status        VARCHAR(20)  NOT NULL DEFAULT 'disabled' COMMENT '开通进度：disabled/provisioning/ready/failed',
    provisioning_message       TEXT         NULL COMMENT 'provisioning 失败原因 / 最近一次结果摘要',
    cert_secret_name           VARCHAR(253) NOT NULL DEFAULT '' COMMENT '通配证书 k8s TLS Secret 名（通配 Ingress 引用）',
    cert_status                VARCHAR(20)  NOT NULL DEFAULT 'none' COMMENT '证书状态：none/issuing/issued/renewing/failed',
    cert_not_after             DATETIME(6)  NULL COMMENT '证书到期时间（续期巡检依据）',
    cert_last_issued_at        DATETIME(6)  NULL COMMENT '最近一次签发成功时间',
    cert_last_renewed_at       DATETIME(6)  NULL COMMENT '最近一次续签成功时间',
    cert_message               TEXT         NULL COMMENT '证书失败原因 / 最近一次结果摘要',
    created_at                 DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间',
    updated_at                 DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '更新时间',
    PRIMARY KEY (org_id),
    CONSTRAINT fk_owpc_org FOREIGN KEY (org_id) REFERENCES organizations(id),
    CONSTRAINT owpc_provisioning_status_check CHECK (provisioning_status IN ('disabled','provisioning','ready','failed')),
    CONSTRAINT owpc_cert_status_check CHECK (cert_status IN ('none','issuing','issued','renewing','failed')),
    CONSTRAINT owpc_dns_provider_check CHECK (dns_provider IN ('','alidns','huaweicloud','tencentcloud','cmcccloud'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='企业网站发布能力配置与证书托管状态';
